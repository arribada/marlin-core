// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/arribada/marlin-core/collector/pkg/logger"
	"github.com/arribada/marlin-core/collector/pkg/types"
	klog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
)

const (
	ComponentName = "capture controller"

	startPrefix = "/start"
	stopPrefix  = "/stop"
)

type Config struct {
	Enabled        bool
	Verbose        bool
	Host           string
	TranscriberURL string
}

func New(lgr klog.Logger, ctx context.Context, cfg Config, theChans *types.Chans) (*Controller, error) {
	lgr, err := logger.ApplyFilter("info", lgr)
	if err != nil {
		return nil, err
	}
	ctx, close := context.WithCancel(ctx)

	j := &Controller{
		klogger:             klog.With(lgr, "component", ComponentName),
		ctx:                 ctx,
		chans:               theChans,
		close:               close,
		host:                cfg.Host,
		audioTranscriberURL: cfg.TranscriberURL,
	}

	return j, nil
}

type Controller struct {
	klogger             klog.Logger
	ctx                 context.Context
	chans               *types.Chans
	close               context.CancelFunc
	host                string
	audioTranscriberURL string
}

// TODO: it'd be nice to reuse that code for all our messages listeners,
// but oh well.
func (c *Controller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = level.Info(c.klogger).Log("msg", "got button request")

	if r.URL.Path != startPrefix && r.URL.Path != stopPrefix {
		http.Error(w, "invalid command", 404)
	}

	if r.URL.Path == startPrefix {
		c.buttonPressEvent(true)
		c.handleStart()
	}

	if r.URL.Path == stopPrefix {
		c.buttonPressEvent(false)
		c.handleStop()
	}

	_, _ = w.Write([]byte("OK"))
}

// instructs the watcher to create a button press event (file).
func (c *Controller) buttonPressEvent(start bool) {
	select {
	case c.chans.ButtonPress <- start:
		_ = level.Debug(c.klogger).Log("msg", "button press event sent to watcher")
	case <-time.After(time.Second):
		_ = level.Debug(c.klogger).Log("msg", "time out on sending button press event")
	}
}

func (c *Controller) Run() error {
	_ = level.Info(c.klogger).Log("msg", "starting")

	mux := http.NewServeMux()
	mux.Handle("/", c)
	errC := make(chan error)
	go func() {
		_ = level.Info(c.klogger).Log("msg", fmt.Sprintf("Starting to serve for HTTP for start/stop signals on %s", c.host))
		if err := http.ListenAndServe(c.host, mux); err != nil {
			select {
			case errC <- fmt.Errorf("while starting HTTP server for start/stop signals: %v", err):
			case <-time.After(time.Second):
			}
		}
	}()

	select {
	case err := <-errC:
		return err
	case <-c.ctx.Done():
		return nil
	}
}

func (c *Controller) handleStart() {
	if err := c.startAudio(); err != nil {
		logger.LogAndSend(fmt.Sprintf("while calling audio recorder start: %v", err))
	}
	if err := c.videoRec(true); err != nil {
		logger.LogAndSend(fmt.Sprintf("while calling video recorder start: %v", err))
	}
}

func (c *Controller) handleStop() {
	if err := c.stopAudio(); err != nil {
		logger.LogAndSend(fmt.Sprintf("while calling audio recorder stop: %v", err))
	}
	if err := c.videoRec(false); err != nil {
		logger.LogAndSend(fmt.Sprintf("while calling video recorder stop: %v", err))
	}
}

func (c *Controller) startAudio() error {
	return c.toggleAudioRecording(true)
}

func (c *Controller) stopAudio() error {
	return c.toggleAudioRecording(false)
}

func (c *Controller) toggleAudioRecording(start bool) error {
	if c.audioTranscriberURL == "" {
		return nil
	}
	pathPrefix := "/start"
	if !start {
		pathPrefix = "/stop"
	}
	res, err := http.Get(c.audioTranscriberURL + pathPrefix)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	code := res.StatusCode
	if code != 200 {
		return fmt.Errorf("unexpected code from audio recorder: %v", code)
	}
	return nil
}

func (c *Controller) videoRec(start bool) error {
	if c.chans.VideoCtrl == nil {
		return nil
	}

	select {
	case c.chans.VideoCtrl <- start:
		_ = level.Debug(c.klogger).Log("msg", "video recorder request sent")
		select {
		case c.chans.VideoState <- start:
			return nil
		case <-time.After(2 * time.Second):
			return errors.New("could not propagate video state in time")
		}
	case <-time.After(time.Second):
		return errors.New("time out on sending video recorder request")
	}
}

func (c *Controller) Stop() {
	c.close()

	_ = level.Info(c.klogger).Log("msg", "stopped")
}
