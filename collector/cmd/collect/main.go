// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/arribada/marlin-common/pkg/utils"
	"github.com/arribada/marlin-core/collector/pkg/config"
	"github.com/arribada/marlin-core/collector/pkg/controller"
	"github.com/arribada/marlin-core/collector/pkg/gui"
	"github.com/arribada/marlin-core/collector/pkg/logger"
	"github.com/arribada/marlin-core/collector/pkg/lpm"
	"github.com/arribada/marlin-core/collector/pkg/node/gpsr"
	"github.com/arribada/marlin-core/collector/pkg/node/videor/rtsp"
	"github.com/arribada/marlin-core/collector/pkg/types"
	"github.com/arribada/marlin-core/collector/pkg/watcher"
	"github.com/mpl/basicauth"
)

var theChans types.Chans

func main() {
	cfg, err := config.Parse()
	if err != nil {
		log.Fatalf("while loading configuration: %v", err)
	}
	logger.LogAndSend(fmt.Sprintf("configuration loaded:\n%+v", cfg))

	if cfg.Captain != "" {
		if err := utils.WaitForDrive(cfg.Captain); err != nil {
			log.Fatal(err)
		}
	}

	go getReport(cfg.ReportURL)

	var wg sync.WaitGroup
	startServices(context.Background(), &wg, cfg)

	wg.Wait()
	log.Printf("Collector terminated")
}

// all the services are independent from each other, and each run
// in their own goroutine, with the same child context.
// The signal handler is the only one which can cancel (all) the
// others, through the cancellation function of the parent context.
func startServices(ctx context.Context, wg *sync.WaitGroup, cfg *config.Config) {
	subCtx, cancel := context.WithCancel(ctx)
	// runSignalHandler should always be first
	wg.Add(1)
	go func() {
		defer wg.Done()
		runSignalHandler(cancel)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runConfigserver(subCtx, cfg)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runSatComms(subCtx, cfg.Sat)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runGPS(subCtx, cfg.GPSr)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runVideo(subCtx, cfg.VideorRTSP)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runDataWatcher(subCtx, cfg.DataWatcher)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runController(subCtx, cfg.Controller)
	}()
}

func retry(ctx context.Context, serviceName string, do func()) {
	// TODO: maybe add some jitter, but we only have a handful of services
	// at most restarting at the same time, so no big deal.
	pause := time.Second
	maxPause := 5 * time.Minute
	for {
		select {
		case <-ctx.Done():
			log.Printf("retry loop for %v interrupted by context", serviceName)
			return
		default:
		}
		do()
		log.Printf("retrying %v in %fs", serviceName, pause.Seconds())
		select {
		case <-ctx.Done():
			log.Printf("retry loop for %v interrupted by context", serviceName)
			return
		case <-time.After(pause):
		}
		pause *= 2
		if pause > maxPause {
			pause = maxPause
		}
	}
}

func runSignalHandler(cancel context.CancelFunc) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(c)
	sig := <-c
	log.Printf("Interrupted by signal: %v", sig)
	cancel()
}

func runConfigserver(ctx context.Context, cfg *config.Config) {
	// TODO: all in a retry as well?
	srv := gui.New(":8080", cfg)

	idleConnsClosed := make(chan struct{})
	go func() {
		<-ctx.Done()
		log.Printf("shutting down config server")
		if err := srv.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			log.Printf("while shutting down config server: %v", err)
		}
		close(idleConnsClosed)
	}()

	log.Printf("Starting web server on %v", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("config server error: %v", err)
		return
	}
	log.Printf("waiting for idle connections")
	<-idleConnsClosed
	log.Printf("idle connections terminated")
}

func runSatComms(ctx context.Context, cfg lpm.Config) {
	if !cfg.Enabled {
		return
	}
	retry(ctx, "Sat comms", func() {
		if err := lpm.Run(cfg, ctx); err != nil {
			log.Printf("sat comms error: %v", err)
		}
	})
}

func runGPS(ctx context.Context, cfg gpsr.Config) {
	if !cfg.Enabled {
		return
	}
	theChans.Position = make(chan types.Position)
	retry(ctx, "GPS recorder", func() {
		gps, err := gpsr.New(cfg, theChans.Position)
		if err != nil {
			log.Printf("while initializing GPS recorder: %v", err)
			return
		}
		if err := gps.Run(ctx); err != nil {
			log.Printf("gps recorder error: %v", err)
		}
	})
}

func runVideo(ctx context.Context, cfg rtsp.Config) {
	if !cfg.Enabled {
		return
	}
	theChans.VideoCtrl = make(chan bool)
	retry(ctx, "Video recorder", func() {
		vid, err := rtsp.New(cfg, theChans.VideoCtrl)
		if err != nil {
			log.Printf("while initializing Video recorder: %v", err)
			return
		}

		if err := vid.Run(ctx); err != nil {
			log.Printf("video recorder error: %v", err)
		}
	})
}

func runDataWatcher(ctx context.Context, cfg watcher.Config) {
	if !cfg.Enabled {
		return
	}
	theChans.VideoState = make(chan bool)
	theChans.ButtonPress = make(chan bool)
	retry(ctx, "Data Watcher", func() {
		dw, err := watcher.New(cfg, &theChans)
		if err != nil {
			log.Printf("while initializing data watcher: %v", err)
			return
		}

		if err := dw.Run(ctx); err != nil {
			log.Printf("data watcher error: %v", err)
		}
	})
}

func runController(ctx context.Context, cfg controller.Config) {
	if !cfg.Enabled {
		return
	}
	retry(ctx, "Controller Handler", func() {
		ctrl, err := controller.New(cfg, &theChans)
		if err != nil {
			log.Printf("while initializing the start/stop controller: %v", err)
			return
		}

		if err := ctrl.Run(ctx); err != nil {
			log.Printf("controller handler error: %v", err)
		}
	})
}

func getReport(reportURL string) {
	if reportURL == "" {
		return
	}
	// NewUserPass automatically checks for AUTH_USERPASS
	up, err := basicauth.NewUserPass("")
	if err != nil {
		log.Println("getReport: ", err.Error())
		return
	}

	time.Sleep(30 * time.Second)
	req, err := http.NewRequest("GET", reportURL, nil)
	if err != nil {
		log.Println("getReport: ", err.Error())
		return
	}
	req.SetBasicAuth(up.U, up.P)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("getReport: ", err.Error())
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("getReport: ", err.Error())
		return
	}
	// TODO: make it all in one line for goog cloud?
	log.Println("Status report:\n", string(data))
}

func ExitOnErr(err error, msg string) {
	if err != nil {
		pc, _, _, ok := runtime.Caller(2)
		details := runtime.FuncForPC(pc)
		caller := ""
		if ok && details != nil {
			caller = details.Name()
		}
		log.Fatalf("root execution error:%v msg:%v caller:%v", err, msg, caller)
	}
}
