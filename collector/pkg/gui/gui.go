// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package gui

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/arribada/marlin-core/collector/pkg/config"
	"github.com/arribada/marlin-core/collector/pkg/watcher"
)

type cfgHandler struct {
	cfg *config.Config
}

func (c *cfgHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, err := json.Marshal(c.cfg)
	if err != nil {
		log.Printf("while encoding runtime config: %v", err)
		http.Error(w, "error while encoding runtime config", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

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
	if err := d.w.Update(); err != nil {
		log.Printf("while updating du watcher: %v", err)
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
		log.Printf("while encoding storage report: %v", err)
		http.Error(w, "error while encoding storage report", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

type server struct {
	cfg cfgHandler
	duh *duHandler
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if r.URL.Path == "/config" {
		s.cfg.ServeHTTP(w, r)
		return
	}

	if r.URL.Path == "/du" {
		s.duh.ServeHTTP(w, r)
		return
	}

	http.NotFound(w, r)
}

func New(addr string, cfg *config.Config) *http.Server {
	// Create a new HTTP server with the custom handler

	var duh *duHandler
	if cfg.DataWatcher.Enabled {
		duh = &duHandler{w: watcher.NewDuWatcher(cfg.DataWatcher)}
	}
	return &http.Server{
		Addr: addr,
		Handler: &server{
			cfg: cfgHandler{cfg: cfg},
			duh: duh,
		},
	}
}
