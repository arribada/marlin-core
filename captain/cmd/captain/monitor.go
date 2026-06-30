// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"context"
	"log"
	"os"
	"time"
)

func monitorTemperature(ctx context.Context) {
	if flagTempInterval <= 0 {
		log.Printf("temperature monitoring disabled")
		return
	}
	interval := time.Duration(flagTempInterval) * time.Second
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			data, err := os.ReadFile("/sys/devices/virtual/thermal/thermal_zone0/temp")
			if err != nil {
				log.Printf("while reading temperature: %v", err)
			} else {
				log.Printf("temperature: %s", string(data))
			}
			time.Sleep(interval)
			// TODO: add watchdog?
		}
	}()
}
