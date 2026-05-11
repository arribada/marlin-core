// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package controller

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/arribada/marlin-core/collector/pkg/types"
)

var logger = log.New(os.Stderr, "Controller handler: ", log.Default().Flags()|log.Lmsgprefix)

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

func New(cfg Config, theChans *types.Chans) (*Controller, error) {
	j := &Controller{
		chans:               theChans,
		host:                cfg.Host,
		audioTranscriberURL: cfg.TranscriberURL,
		verbose:             cfg.Verbose,
	}

	return j, nil
}

type Controller struct {
	ctx                 context.Context
	chans               *types.Chans
	host                string
	audioTranscriberURL string
	verbose             bool
}

func (c *Controller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != startPrefix && r.URL.Path != stopPrefix {
		http.Error(w, "invalid command", 404)
		return
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
		if c.verbose {
			logger.Printf("button press event sent to watcher")
		}
	case <-time.After(time.Second):
		if c.verbose {
			logger.Printf("time out on sending button press event")
		}
	}
}

func (c *Controller) Run(ctx context.Context) error {
	logger.Printf("starting")

	mux := http.NewServeMux()
	mux.Handle("/", c)
	srv := http.Server{
		Addr:    c.host,
		Handler: mux,
	}

	idleConnsClosed := make(chan struct{})
	go func() {
		<-ctx.Done()
		logger.Printf("shutting down")
		if err := srv.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			logger.Printf("while shutting: %v", err)
		}
		close(idleConnsClosed)
	}()

	logger.Printf("Starting to serve for HTTP for start/stop signals on %s", c.host)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("while starting to serve: %v", err)
	}
	logger.Printf("waiting for idle connections")
	<-idleConnsClosed
	logger.Printf("idle connections terminated")
	return nil
}

func (c *Controller) handleStart() {
	if err := c.startAudio(); err != nil {
		logger.Printf("while calling audio recorder start: %v", err)
	}
	if err := c.videoRec(true); err != nil {
		logger.Printf("while calling video recorder start: %v", err)
	}
}

func (c *Controller) handleStop() {
	if err := c.stopAudio(); err != nil {
		logger.Printf("while calling audio recorder stop: %v", err)
	}
	if err := c.videoRec(false); err != nil {
		logger.Printf("while calling video recorder stop: %v", err)
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
		if c.verbose {
			logger.Printf("video recorder request sent")
		}
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
