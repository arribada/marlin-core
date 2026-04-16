// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"errors"
	"flag"
	"log"
	"os"
	"time"

	"github.com/arribada/marlin-common/pkg/utils"
	"github.com/arribada/marlin-core/gc/pkg/gc"
)

var (
	flagSource        string
	flagRootDir       string
	flagStdout        bool
	flagCaptain       string
	flagNoBracket     bool
	flagHost          string
	flagVerbose       bool
	flagGCGracePeriod int // in seconds
	flagGCInterval    int // in seconds
	flagThreshold     float64
	flagAgeLimit      int64
	flagVessel        string
	flagConnString    string
	flagAggressive    bool
	flagDry           bool
)

func init() {
	flag.StringVar(&flagRootDir, "rootdir", "/perm/marlin", "data directory")
	flag.BoolVar(&flagVerbose, "v", false, "be verbose")
	flag.BoolVar(&flagDry, "dry", true, "dry run")
	flag.BoolVar(&flagAggressive, "aggressive", false, "run a second time in aggressive mode if first pass is not enough")
	flag.StringVar(&flagCaptain, "captain", "localhost:7143", "wait for captain (to have mounted an external drive at this host:port. Skip if empty.")
	flag.IntVar(&flagGCGracePeriod, "gcgrace", 86400, "the period (in seconds) during which an audio file, after it's been created, is excluded from garbage collection")
	flag.IntVar(&flagGCInterval, "gcinterval", 600, "the interval in between subsequent garbage collection runs")
	flag.Float64Var(&flagThreshold, "threshold", 0.9, "the storage used quota target for the garbage collector. garbage collection runs if storage usage is above this threshold, and is stopped as soon as usage goes back below.")
	flag.StringVar(&flagVessel, "vessel", "", "vessel name on Azure")
	flag.StringVar(&flagConnString, "conn", "", "Azure connection string")
	// TODO: use something more user-friendly later
	flag.Int64Var(&flagAgeLimit, "age", 2678400e9, "the age limit for aggressive removal. defaults to a month.")
}

func main() {
	flag.Parse()

	if err := checkFlags(); err != nil {
		log.Fatal(err)
	}

	if err := initOutDir(); err != nil {
		log.Fatal(err)
	}

	if err := runGCs(); err != nil {
		log.Fatalf("Garbage Collector failed: %v", err)
	}
}

func checkFlags() error {
	if flagConnString == "" {
		flagConnString = os.Getenv("AZURE_CONN")
	}
	if flagConnString == "" {
		return errors.New("Azure connection string (AZURE_CONN) not specified")
	}

	if flagVessel == "" {
		flagVessel = os.Getenv("AZURE_SHARE")
	}
	if flagVessel == "" {
		return errors.New("vessel name (AZURE_SHARE) not specified")
	}

	return nil
}

func initOutDir() error {
	if flagCaptain == "" {
		return os.MkdirAll(flagRootDir, 0750)
	}

	return utils.WaitForDrive(flagCaptain)
}

// Every flagGCInterval,
// runGCs starts a generic collector pass, if needed.
func runGCs() error {
	for {
		gg := gc.GGC{
			ConnString: flagConnString,
			Vessel:     flagVessel,
			RootDir:    flagRootDir,
			Verbose:    flagVerbose,
			Threshold:  flagThreshold,
			Aggressive: false,
			AgeLimit:   time.Duration(flagAgeLimit),
			Dry:        flagDry,
		}
		log.Printf("Starting a generic GC run")
		if err := gg.Run(); err != nil {
			return err
		}

		time.Sleep(time.Duration(flagGCInterval) * time.Second)
	}
}
