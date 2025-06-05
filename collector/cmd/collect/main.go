// Copyright (c) The Arribada initiative.
// Licensed under the MIT License.

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"syscall"

	"github.com/arribada/marlin-core/collector/pkg/config"
	"github.com/arribada/marlin-core/collector/pkg/controller"
	"github.com/arribada/marlin-core/collector/pkg/gui"
	"github.com/arribada/marlin-core/collector/pkg/logger"
	"github.com/arribada/marlin-core/collector/pkg/node/gpsr"
	"github.com/arribada/marlin-core/collector/pkg/node/videor/rtsp"
	"github.com/arribada/marlin-core/collector/pkg/node/watcher"
	"github.com/arribada/marlin-core/collector/pkg/types"
	"github.com/arribada/marlin-core/common/pkg/utils"

	"github.com/go-kit/kit/log/level"
	"github.com/oklog/run"
)

var theChans types.Chans

func main() {

	lgr := logger.NewLogger()
	ctxGlobal := context.Background()

	//	cfg, err := config.Parse(cli.ConfigPath)
	// ExitOnErr(err, "while loading config")
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

	var g run.Group

	// Run groups.
	{
		g.Add(run.SignalHandler(context.Background(), syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM))

		// Serve our config and other utilities.
		{
			srv := gui.New(":8080", cfg)
			g.Add(func() error {
				_ = level.Info(lgr).Log("msg", "starting web server", "addr", srv.Addr)
				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					return err
				}
				return nil
			}, func(error) {
				if err := srv.Close(); err != nil {
					log.Printf("while closing srv: %v", err)
				}
			})
		}

		// GPS recorder.
		if cfg.GPSr.Enabled {
			gps, err := gpsr.New(lgr, cfg.GPSr)
			if err != nil {
				log.Fatalf("while initializing GPS recorder: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())

			g.Add(func() error {
				if err := gps.Run(ctx); err != nil {
					return fmt.Errorf("gps recorder error: %v", err)
				}
				return nil
			}, func(error) {
				gps.Stop(cancel)
			})
		}

		// Video RTSP recorder.
		if cfg.VideorRTSP.Enabled {
			theChans.VideoCtrl = make(chan bool)
			vid, err := rtsp.New(lgr, cfg.VideorRTSP, theChans.VideoCtrl)
			if err != nil {
				log.Fatalf("while initializing videor recorder: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())

			g.Add(func() error {
				if err := vid.Run(ctx); err != nil {
					return fmt.Errorf("video recorder error: %v", err)
				}
				return nil
			}, func(error) {
				vid.Stop(cancel)
			})
		}

		// Data watcher
		if cfg.DataWatcher.Enabled {
			theChans.VideoState = make(chan bool)
			theChans.ButtonPress = make(chan bool)
			dw, err := watcher.New(lgr, cfg.DataWatcher, &theChans)
			if err != nil {
				log.Fatalf("while initializing data watcher: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())

			g.Add(func() error {
				if err := dw.Run(ctx); err != nil {
					return fmt.Errorf("data watcher error: %v", err)
				}
				return nil
			}, func(error) {
				dw.Stop(cancel)
			})
		}

		// Signal handler, and start/stop controller.
		if cfg.Controller.Enabled {
			ctrl, err := controller.New(lgr, ctxGlobal, cfg.Controller, &theChans)
			if err != nil {
				log.Fatalf("while initializing the start/stop controller: %v", err)
			}
			g.Add(func() error {
				if err := ctrl.Run(); err != nil {
					return fmt.Errorf("controller handler error: %v", err)
				}
				return nil
			}, func(error) {
				ctrl.Stop()
			})
		}

		if err := g.Run(); err != nil {
			// Use %+v for github.com/pkg/errors error to print with stack.
			// log.Printf("%+v\n", errors.Wrapf(err, "run group stacktrace"))
			log.Fatalf("%v\n", err)
		}
	}

	_ = level.Info(lgr).Log("msg", "exiting")
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
