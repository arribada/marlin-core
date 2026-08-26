// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package collector

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
	"github.com/arribada/marlin-core/collector/pkg/lpm/battery"
	"github.com/arribada/marlin-core/collector/pkg/types"
	"github.com/arribada/marlin-core/collector/pkg/videor/rtsp"
	"github.com/arribada/marlin-core/collector/pkg/watcher"
	"github.com/mpl/basicauth"
)

var Logger = log.New(os.Stderr, "Collector: ", log.Default().Flags()|log.Lmsgprefix)

var (
	theChans types.Chans

	harbourModeMu sync.RWMutex
	harbourMode   bool
)

func Collect(cfg *config.Config) error {
	utils.LogAndDash(Logger, fmt.Sprintf("configuration loaded:\n%+v", cfg))
	if cfg.Captain != "" {
		if err := utils.WaitForDrive(cfg.Captain, 5*time.Minute); err != nil {
			Logger.Fatalf("while waiting for captain: %v", err)
		}
	}

	// TODO: maybe move to main?
	go func() {
		if err := getReport(cfg.ReportURL); err != nil {
			utils.LogAndDash(Logger, fmt.Sprintf("getReport error: %v", err))
		}
	}()

	var wg sync.WaitGroup
	startServices(context.Background(), &wg, cfg)

	wg.Wait()
	utils.LogAndDash(Logger, fmt.Sprintf("terminated"))

	return nil
}

// startServices sets up different groups of services, through the
// use of contexes and cancel funcs. When power switches to the internal
// battery (as reported by the LPM), a (defined) subset of all services gets
// cancelled, and does not get restarted. The other subset does not get cancelled.
// On low battery, all services get definitively cancelled (by the LPM service).
// Also, all services get definitively cancelled (by the signal handler service),
// on SIGHUP, SIGINT, or SIGTERM.
// See the retry func doc for when services get restarted otherwise.
func startServices(ctx context.Context, wg *sync.WaitGroup, cfg *config.Config) {
	// We use that globalCancel in the signal handler to shut down
	// absolutely everything.
	retryCtx, globalCancel := context.WithCancel(ctx)
	// And we create a subgroup for the services that get shutdown when
	// we go to harbour mode (on battery power).
	harbourCtx, harbourCancel := context.WithCancel(retryCtx)

	// runSignalHandler should always be first
	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			utils.LogAndDash(Logger, fmt.Sprintf("Routine terminated: signal handler"))
		}()
		runSignalHandler(globalCancel)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			utils.LogAndDash(Logger, fmt.Sprintf("Routine terminated: config server"))
		}()
		runConfigserver(harbourCtx, cfg)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			utils.LogAndDash(Logger, fmt.Sprintf("Routine terminated: LPM"))
		}()
		runLPM(retryCtx, globalCancel, harbourCancel, cfg.Sat)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			utils.LogAndDash(Logger, fmt.Sprintf("Routine terminated: GPS"))
		}()
		runGPS(retryCtx, harbourCtx, cfg.GPSr)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			utils.LogAndDash(Logger, fmt.Sprintf("Routine terminated: video"))
		}()
		runVideo(retryCtx, harbourCtx, cfg.VideorRTSP)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			utils.LogAndDash(Logger, fmt.Sprintf("Routine terminated: data watcher"))
		}()
		runDataWatcher(retryCtx, cfg.DataWatcher)
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			utils.LogAndDash(Logger, fmt.Sprintf("Routine terminated: controller handler"))
		}()
		runController(retryCtx, harbourCtx, cfg.Controller)
	}()
}

func setHarbourMode(cancel context.CancelFunc) error {
	utils.LogAndDash(Logger, "Switching to harbour mode")
	harbourModeMu.Lock()
	harbourMode = true
	harbourModeMu.Unlock()
	// Make all services die cleanly, and then only the ones allowed
	// in harbour mode will restart themselves
	cancel()
	return nil
}

func setAtSeaMode(cancel context.CancelFunc) error {
	utils.LogAndDash(Logger, "Switching to mains mode (restarting from scratch)")
	// kinda useless/overkill since we're restarting anyway...
	harbourModeMu.Lock()
	harbourMode = false
	harbourModeMu.Unlock()
	cancel()
	time.Sleep(10 * time.Second)
	// restart from scratch, let gokrazy restart us
	os.Exit(1)
	// TODO: implement in a more subtle way?
	// Problem: need to recreate and reassign a new harbourCtx to everyone, no?
	return nil
}

func setShutdownMode(cancel context.CancelFunc) error {
	// TODO: what about other processes, such as the transcriber,
	// captain, etc.? Not super important, as we mostly want to cleanly
	// finish our writes to disk.
	utils.LogAndDash(Logger, "Switching to shutdown mode")

	// Make everything shutdown cleanly
	cancel()
	time.Sleep(10 * time.Second)
	shutdown()
	return nil
}

var noRetry = errors.New("no more retries")

// retry retries do() until interrupted by ctx or harbourCtx.
// if do() returns noRetry, retry returns nil.
// if do() returns nil, retry retries do immediately.
// if do() returns an error, retry retries do() after an increasingly longer pause.
func retry(ctx, harbourCtx context.Context, serviceName string, do func() error) {
	// TODO: maybe add some jitter, but we only have a handful of services
	// at most restarting at the same time, so no big deal.
	pause := time.Second
	maxPause := 5 * time.Minute
	for {
		harbourModeMu.RLock()
		if harbourMode {
			// if it's a service allowed to run in harbour mode (DataWatcher),
			// then harbourCtx is the same as ctx, and this does nothing.
			select {
			case <-harbourCtx.Done():
				harbourModeMu.RUnlock()
				utils.LogAndDash(Logger, fmt.Sprintf("retry loop for %v interrupted by context", serviceName))
				return
			default:
			}
		}
		harbourModeMu.RUnlock()
		select {
		case <-ctx.Done():
			utils.LogAndDash(Logger, fmt.Sprintf("retry loop for %v interrupted by global context", serviceName))
			return
		default:
		}
		err := do()
		if err == noRetry {
			utils.LogAndDash(Logger, fmt.Sprintf("terminating retries for service: %v", serviceName))
			return
		}
		if err == nil {
			// reset the pause on successful run
			pause = time.Second
		}
		utils.LogAndDash(Logger, fmt.Sprintf("retrying %v in %fs", serviceName, pause.Seconds()))
		select {
		case <-ctx.Done():
			utils.LogAndDash(Logger, fmt.Sprintf("retry loop for %v interrupted by context", serviceName))
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
	utils.LogAndDash(Logger, fmt.Sprintf("Interrupted by signal: %v", sig))
	cancel()
}

func runConfigserver(ctx context.Context, cfg *config.Config) {
	// TODO: all in a retry as well?
	srv := gui.New("", cfg)

	idleConnsClosed := make(chan struct{})
	go func() {
		<-ctx.Done()
		Logger.Printf("shutting down config server")
		if err := srv.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			Logger.Printf("while shutting down config server: %v", err)
		}
		close(idleConnsClosed)
	}()

	Logger.Printf("Starting web server on %v", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		utils.LogAndDash(Logger, fmt.Sprintf("config server error: %v", err))
		return
	}
	Logger.Printf("Config server: waiting for idle connections")
	<-idleConnsClosed
	Logger.Printf("Config server: idle connections terminated")
}

func runLPM(retryCtx context.Context, globalCancel, harbourCancel context.CancelFunc, cfg lpm.Config) {
	if !cfg.Enabled {
		return
	}

	// retryCtx as second argument because we want lpm to stay alive in harbour mode
	retry(retryCtx, retryCtx, "LPM", func() error {
		harbourModeMu.RLock()
		hbm := harbourMode
		harbourModeMu.RUnlock()
		err := lpm.Run(cfg, hbm, retryCtx)
		if err == nil {
			return nil
		}
		utils.LogAndDash(Logger, fmt.Sprintf("LPM error: %v", err))

		if errors.Is(err, battery.LowVoltageErr) {
			if err := setShutdownMode(globalCancel); err != nil {
				Logger.Printf("error while setting shutdown mode: %v", err)
			}
			return noRetry
		}
		if errors.Is(err, battery.OnBatteryErr) {
			if err := setHarbourMode(harbourCancel); err != nil {
				Logger.Printf("error while setting harbour mode: %v", err)
			}
			return nil
		}
		if errors.Is(err, battery.OnMainsErr) {
			if err := setAtSeaMode(globalCancel); err != nil {
				Logger.Printf("error while exiting harbour mode: %v", err)
			}
			return nil
		}

		return err
	})
}

func runGPS(retryCtx, harbourCtx context.Context, cfg gpsr.Config) {
	if !cfg.Enabled {
		return
	}
	theChans.Position = make(chan types.Position)
	retry(retryCtx, harbourCtx, "GPS recorder", func() error {
		gps, err := gpsr.New(cfg, theChans.Position)
		if err != nil {
			utils.LogAndDash(Logger, fmt.Sprintf("while initializing GPS recorder: %v", err))
			return err
		}
		if err := gps.Run(harbourCtx); err != nil {
			utils.LogAndDash(Logger, fmt.Sprintf("gps recorder error: %v", err))
			return err
		}
		return nil
	})
}

func runVideo(retryCtx, harbourCtx context.Context, cfg rtsp.Config) {
	if !cfg.Enabled {
		return
	}
	theChans.VideoCtrl = make(chan bool)
	retry(retryCtx, harbourCtx, "Video recorder", func() error {
		vid, err := rtsp.New(cfg, theChans.VideoCtrl)
		if err != nil {
			utils.LogAndDash(Logger, fmt.Sprintf("while initializing Video recorder: %v", err))
			return err
		}

		err = vid.Run(harbourCtx)
		if err != nil {
			utils.LogAndDash(Logger, fmt.Sprintf("video recorder error: %v", err))
		}
		if !vid.Forever {
			return noRetry
		}
		return err
	})
}

func runDataWatcher(retryCtx context.Context, cfg watcher.Config) {
	if !cfg.Enabled {
		return
	}
	theChans.VideoState = make(chan bool)
	theChans.ButtonPress = make(chan bool)
	// retryCtx as second argument because we want watcher (uploader really) to stay alive in harbour mode
	retry(retryCtx, retryCtx, "Data Watcher", func() error {
		dw, err := watcher.New(cfg, &theChans)
		if err != nil {
			utils.LogAndDash(Logger, fmt.Sprintf("while initializing data watcher: %v", err))
			return err
		}

		if err := dw.Run(retryCtx); err != nil {
			utils.LogAndDash(Logger, fmt.Sprintf("data watcher error: %v", err))
			return err
		}
		return nil
	})
}

func runController(retryCtx, harbourCtx context.Context, cfg controller.Config) {
	if !cfg.Enabled {
		return
	}
	retry(retryCtx, harbourCtx, "Controller Handler", func() error {
		ctrl, err := controller.New(cfg, &theChans)
		if err != nil {
			utils.LogAndDash(Logger, fmt.Sprintf("while initializing the start/stop controller: %v", err))
			return err
		}

		if err := ctrl.Run(harbourCtx); err != nil {
			utils.LogAndDash(Logger, fmt.Sprintf("controller handler error: %v", err))
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
	Logger.Println("Status report:\n", string(data))
	return nil
}
