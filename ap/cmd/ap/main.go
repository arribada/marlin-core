// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"flag"
	"log"
	"net/http"
	"sync"
	"time"
)

var (
	hostapdConf string

	flagHost string

	flagDummy bool

	statusMu sync.RWMutex
	status   bool
)

func init() {
	flag.StringVar(&flagHost, "host", "localhost:2848", "our own HTTP server to report on status")
	flag.BoolVar(&flagDummy, "dummy", false, "Only run a status handler that says OK all the time.")
}

func main() {
	flag.Parse()

	http.HandleFunc("/status", statusHandler)
	go func() {
		log.Fatal(http.ListenAndServe(flagHost, nil))
	}()

	if flagDummy {
		statusMu.Lock()
		status = true
		statusMu.Unlock()
		select {}
	}

	if err := checkConf(); err != nil {
		log.Fatal(err)
	}

	if err := loadModules(); err != nil {
		log.Fatal(err)
	}

	if err := checkInterface(); err != nil {
		log.Fatal(err)
	}

	if err := setupInterface(); err != nil {
		log.Fatal(err)
	}

	go func() {
		if err := startHostapd(); err != nil {
			log.Fatal(err)
		}
		time.Sleep(10 * time.Second)
	}()

	go func() {
		if err := startDHCPD(); err != nil {
			log.Fatal(err)
		}
	}()
	time.Sleep(time.Second)
	statusMu.Lock()
	status = true
	statusMu.Unlock()
	select {}
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	statusMu.RLock()
	defer statusMu.RUnlock()
	if status {
		w.Write([]byte("OK"))
		return
	}
	w.Write([]byte("KO"))
}
