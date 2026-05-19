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
	"github.com/arribada/marlin-core/collector/pkg/gpsr"
	"github.com/arribada/marlin-core/collector/pkg/gui"
	"github.com/arribada/marlin-core/collector/pkg/lpm"
	"github.com/arribada/marlin-core/collector/pkg/types"
	"github.com/arribada/marlin-core/collector/pkg/videor/rtsp"
	"github.com/arribada/marlin-core/collector/pkg/watcher"
	"github.com/mpl/basicauth"
)

var logger = log.New(os.Stderr, "Collector: ", log.Default().Flags()|log.Lmsgprefix)

var theChans types.Chans

func main() {
	cfg, err := config.Parse()
	if err != nil {
		logger.Fatalf("while loading configuration: %v", err)
	}
	logger.Printf("configuration loaded:\n%+v", cfg)

	if cfg.Captain != "" {
		if err := utils.WaitForDrive(cfg.Captain, 5*time.Minute); err != nil {
			logger.Fatalf("while waiting for captain: %v", err)
		}
	}

	go func() {
		if err := getReport(cfg.ReportURL); err != nil {
			log.Printf("getReport error: %v", err)
		}
	}()

	var wg sync.WaitGroup
	startServices(context.Background(), &wg, cfg)

	wg.Wait()
	logger.Printf("terminated")
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
		defer func() {
			wg.Done()
			logger.Printf("Routine terminated: signal handler")
		}()
		runSignalHandler(cancel)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			logger.Printf("Routine terminated: config server")
		}()
		runConfigserver(subCtx, cfg)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			logger.Printf("Routine terminated: sat comms")
		}()
		runSatComms(subCtx, cfg.Sat)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			logger.Printf("Routine terminated: GPS")
		}()
		runGPS(subCtx, cfg.GPSr)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			logger.Printf("Routine terminated: video")
		}()
		runVideo(subCtx, cfg.VideorRTSP)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			logger.Printf("Routine terminated: data watcher")
		}()
		runDataWatcher(subCtx, cfg.DataWatcher)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			logger.Printf("Routine terminated: controller handler")
		}()
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
			logger.Printf("retry loop for %v interrupted by context", serviceName)
			return
		default:
		}
		do()
		logger.Printf("retrying %v in %fs", serviceName, pause.Seconds())
		select {
		case <-ctx.Done():
			logger.Printf("retry loop for %v interrupted by context", serviceName)
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
	logger.Printf("Interrupted by signal: %v", sig)
	cancel()
}

func runConfigserver(ctx context.Context, cfg *config.Config) {
	// TODO: all in a retry as well?
	srv := gui.New(":8080", cfg)

	idleConnsClosed := make(chan struct{})
	go func() {
		<-ctx.Done()
		logger.Printf("shutting down config server")
		if err := srv.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			logger.Printf("while shutting down config server: %v", err)
		}
		close(idleConnsClosed)
	}()

	logger.Printf("Starting web server on %v", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Printf("config server error: %v", err)
		return
	}
	logger.Printf("Config server: waiting for idle connections")
	<-idleConnsClosed
	logger.Printf("Config server: idle connections terminated")
}

func runSatComms(ctx context.Context, cfg lpm.Config) {
	if !cfg.Enabled {
		return
	}
	retry(ctx, "Sat comms", func() {
		if err := lpm.Run(cfg, ctx); err != nil {
			logger.Printf("LPM error: %v", err)
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
			logger.Printf("while initializing GPS recorder: %v", err)
			return
		}
		if err := gps.Run(ctx); err != nil {
			logger.Printf("gps recorder error: %v", err)
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
			logger.Printf("while initializing Video recorder: %v", err)
			return
		}

		if err := vid.Run(ctx); err != nil {
			logger.Printf("video recorder error: %v", err)
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
			logger.Printf("while initializing data watcher: %v", err)
			return
		}

		if err := dw.Run(ctx); err != nil {
			logger.Printf("data watcher error: %v", err)
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
			logger.Printf("while initializing the start/stop controller: %v", err)
			return
		}

		if err := ctrl.Run(ctx); err != nil {
			logger.Printf("controller handler error: %v", err)
		}
	})
}

func getReport(reportURL string) error {
	if reportURL == "" {
		return nil
	}
	// NewUserPass automatically checks for AUTH_USERPASS
	up, err := basicauth.NewUserPass("")
	if err != nil {
		return err
	}

	time.Sleep(30 * time.Second)
	req, err := http.NewRequest("GET", reportURL, nil)
	if err != nil {
		return fmt.Errorf("while creating request: %v", err)
	}
	req.SetBasicAuth(up.U, up.P)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("while getting report: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("while reading report: %v", err)
	}
	// TODO: make it all in one line for goog cloud?
	logger.Println("Status report:\n", string(data))
	return nil
}

func ExitOnErr(err error, msg string) {
	if err != nil {
		pc, _, _, ok := runtime.Caller(2)
		details := runtime.FuncForPC(pc)
		caller := ""
		if ok && details != nil {
			caller = details.Name()
		}
		logger.Fatalf("root execution error:%v msg:%v caller:%v", err, msg, caller)
	}
}
