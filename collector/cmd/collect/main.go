// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
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
	utils.LogAndDash(logger, fmt.Sprintf("configuration loaded:\n%+v", cfg))

	if cfg.Captain != "" {
		if err := utils.WaitForDrive(cfg.Captain, 5*time.Minute); err != nil {
			logger.Fatalf("while waiting for captain: %v", err)
		}
	}

	go func() {
		if err := getReport(cfg.ReportURL); err != nil {
			utils.LogAndDash(logger, fmt.Sprintf("getReport error: %v", err))
		}
	}()

	var wg sync.WaitGroup
	startServices(context.Background(), &wg, cfg)

	wg.Wait()
	utils.LogAndDash(logger, fmt.Sprintf("terminated"))
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
			utils.LogAndDash(logger, fmt.Sprintf("Routine terminated: signal handler"))
		}()
		runSignalHandler(cancel)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			utils.LogAndDash(logger, fmt.Sprintf("Routine terminated: config server"))
		}()
		runConfigserver(subCtx, cfg)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			utils.LogAndDash(logger, fmt.Sprintf("Routine terminated: LPM"))
		}()
		runLPM(subCtx, cfg.Sat)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			utils.LogAndDash(logger, fmt.Sprintf("Routine terminated: GPS"))
		}()
		runGPS(subCtx, cfg.GPSr)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			utils.LogAndDash(logger, fmt.Sprintf("Routine terminated: video"))
		}()
		runVideo(subCtx, cfg.VideorRTSP)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			utils.LogAndDash(logger, fmt.Sprintf("Routine terminated: data watcher"))
		}()
		runDataWatcher(subCtx, cfg.DataWatcher)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			utils.LogAndDash(logger, fmt.Sprintf("Routine terminated: controller handler"))
		}()
		runController(subCtx, cfg.Controller)
	}()
}

var noRetry = errors.New("no more retries")

func retry(ctx context.Context, serviceName string, do func() error) {
	// TODO: maybe add some jitter, but we only have a handful of services
	// at most restarting at the same time, so no big deal.
	pause := time.Second
	maxPause := 5 * time.Minute
	for {
		select {
		case <-ctx.Done():
			utils.LogAndDash(logger, fmt.Sprintf("retry loop for %v interrupted by context", serviceName))
			return
		default:
		}
		err := do()
		if err == noRetry {
			utils.LogAndDash(logger, fmt.Sprintf("terminating retries for service: %v", serviceName))
			return
		}
		if err == nil {
			// reset the pause on successful run
			pause = time.Second
		}
		utils.LogAndDash(logger, fmt.Sprintf("retrying %v in %fs", serviceName, pause.Seconds()))
		select {
		case <-ctx.Done():
			utils.LogAndDash(logger, fmt.Sprintf("retry loop for %v interrupted by context", serviceName))
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
	utils.LogAndDash(logger, fmt.Sprintf("Interrupted by signal: %v", sig))
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
		utils.LogAndDash(logger, fmt.Sprintf("config server error: %v", err))
		return
	}
	logger.Printf("Config server: waiting for idle connections")
	<-idleConnsClosed
	logger.Printf("Config server: idle connections terminated")
}

func runLPM(ctx context.Context, cfg lpm.Config) {
	if !cfg.Enabled {
		return
	}
	retry(ctx, "Sat comms", func() error {
		if err := lpm.Run(cfg, ctx); err != nil {
			utils.LogAndDash(logger, fmt.Sprintf("LPM error: %v", err))
			return err
		}
		return nil
	})
}

func runGPS(ctx context.Context, cfg gpsr.Config) {
	if !cfg.Enabled {
		return
	}
	theChans.Position = make(chan types.Position)
	retry(ctx, "GPS recorder", func() error {
		gps, err := gpsr.New(cfg, theChans.Position)
		if err != nil {
			utils.LogAndDash(logger, fmt.Sprintf("while initializing GPS recorder: %v", err))
			return err
		}
		if err := gps.Run(ctx); err != nil {
			utils.LogAndDash(logger, fmt.Sprintf("gps recorder error: %v", err))
			return err
		}
		return nil
	})
}

func runVideo(ctx context.Context, cfg rtsp.Config) {
	if !cfg.Enabled {
		return
	}
	theChans.VideoCtrl = make(chan bool)
	retry(ctx, "Video recorder", func() error {
		vid, err := rtsp.New(cfg, theChans.VideoCtrl)
		if err != nil {
			utils.LogAndDash(logger, fmt.Sprintf("while initializing Video recorder: %v", err))
			return err
		}

		err = vid.Run(ctx)
		if err != nil {
			utils.LogAndDash(logger, fmt.Sprintf("video recorder error: %v", err))
		}
		if !vid.Forever {
			return noRetry
		}
		return err
	})
}

func runDataWatcher(ctx context.Context, cfg watcher.Config) {
	if !cfg.Enabled {
		return
	}
	theChans.VideoState = make(chan bool)
	theChans.ButtonPress = make(chan bool)
	retry(ctx, "Data Watcher", func() error {
		dw, err := watcher.New(cfg, &theChans)
		if err != nil {
			utils.LogAndDash(logger, fmt.Sprintf("while initializing data watcher: %v", err))
			return err
		}

		if err := dw.Run(ctx); err != nil {
			utils.LogAndDash(logger, fmt.Sprintf("data watcher error: %v", err))
			return err
		}
		return nil
	})
}

func runController(ctx context.Context, cfg controller.Config) {
	if !cfg.Enabled {
		return
	}
	retry(ctx, "Controller Handler", func() error {
		ctrl, err := controller.New(cfg, &theChans)
		if err != nil {
			utils.LogAndDash(logger, fmt.Sprintf("while initializing the start/stop controller: %v", err))
			return err
		}

		if err := ctrl.Run(ctx); err != nil {
			utils.LogAndDash(logger, fmt.Sprintf("controller handler error: %v", err))
			return err
		}
		return nil
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
