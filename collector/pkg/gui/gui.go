// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package gui

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/arribada/marlin-core/collector/pkg/config"
	lpmio "github.com/arribada/marlin-core/collector/pkg/lpm/io"
	"github.com/arribada/marlin-core/collector/pkg/watcher"
)

var logger = log.New(os.Stderr, "(config) Server: ", log.Default().Flags()|log.Lmsgprefix)

type cfgHandler struct {
	cfg *config.Config
}

func (c *cfgHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	data, err := json.Marshal(c.cfg)
	if err != nil {
		logger.Printf("while encoding runtime config: %v", err)
		http.Error(w, "error while encoding runtime config", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

// TODO: redundant with report from databrowser? Move everything in here to databrowser anyway?
type duHandler struct {
	w *watcher.DuWatcher
}

type StorageReport struct {
	Free  uint64
	Total uint64
	Used  float64
}

func (d *duHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if d == nil {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if err := d.w.Update(); err != nil {
		logger.Printf("while updating du watcher: %v", err)
		http.Error(w, "error while updating du watcher", http.StatusInternalServerError)
		return
	}
	sr := StorageReport{
		Free:  d.w.Free,
		Total: d.w.Total,
		Used:  d.w.Used,
	}

	data, err := json.Marshal(sr)
	if err != nil {
		logger.Printf("while encoding storage report: %v", err)
		http.Error(w, "error while encoding storage report", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

type server struct {
	cfg cfgHandler
	duh *duHandler
	res *resetHandler
}

type resetHandler struct{}

func (res *resetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if res == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "wrong method", http.StatusMethodNotAllowed)
		return
	}

	if err := lpmio.SendReset(); err != nil {
		logger.Printf("while sending reset: %v", err)
		http.Error(w, "while sending reset", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(200)
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/config" {
		s.cfg.ServeHTTP(w, r)
		return
	}

	if r.URL.Path == "/du" {
		s.duh.ServeHTTP(w, r)
		return
	}

	if r.URL.Path == "/reset" {
		s.res.ServeHTTP(w, r)
		return
	}

	http.NotFound(w, r)
}

func New(addr string, cfg *config.Config) *http.Server {
	// Create a new HTTP server with the custom handler

	if addr == "" {
		addr = "localhost:8080"
	}

	var duh *duHandler
	if cfg.DataWatcher.Enabled {
		duh = &duHandler{w: watcher.NewDuWatcher(cfg.DataWatcher)}
	}
	return &http.Server{
		Addr: addr,
		Handler: &server{
			cfg: cfgHandler{cfg: cfg},
			duh: duh,
			res: &resetHandler{},
		},
	}
}
