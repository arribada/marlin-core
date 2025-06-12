// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/arribada/marlin-core/collector/pkg/logger"
	"github.com/arribada/marlin-core/collector/pkg/node/gpsr"
)

var (
	flagDevice string
)

func init() {
	flag.StringVar(&flagDevice, "device", "/dev/cu.usbmodem14101", "GPS capture device")
}

func main() {
	flag.Parse()

	logger := logger.NewLogger()

	dataDir := "./gpsdata"
	log.Printf("Datadir: %v", dataDir)

	gps, err := gpsr.New(logger, gpsr.Config{
		CaptureDevice: flagDevice,
		DataDir:       dataDir,
		BaudRate:      9600,
		// You can tweak that one to control the length/size of each generated .json
		// file. Keep it below RecTimeout though.
		SplitDuration: time.Minute,
	})
	if err != nil {
		log.Fatalf("while initializing GPS recorder: %v", err)
	}

	if err := gps.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
