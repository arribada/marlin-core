// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

// These tests live here, so we can run them on-device, on gokrazy.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/arribada/marlin-core/collector/integration/populate"
	"github.com/arribada/marlin-core/collector/pkg/watcher"
)

var rootDir = "/perm/tests/"

func init() {
	flag.StringVar(&populate.FlagPopulate, "populate", "", "dir to populate with some (fake) data, for e.g. GC tests")
	flag.StringVar(&populate.FlagVessel, "vessel", "", "vessel name on Azure")
	flag.StringVar(&populate.FlagConnString, "conn", "", "Azure connection string")
}

func testProbeVid(name string, shouldFail bool) {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Fatal(err)
	}
	out, err := watcher.ProbeVid(name)
	if shouldFail {
		if err == nil {
			log.Fatal("Should have failed, but did not")
		}
		log.Printf("Failed as expected with: %v", err)
		return
	}
	if err != nil {
		log.Fatalf("%v", err)
	}
	log.Printf("Succeeded as expected with: %v", out)
}

func checkFlags() error {
	if populate.FlagConnString == "" {
		populate.FlagConnString = os.Getenv("AZURE_CONN")
	}
	return nil
}

func main() {
	flag.Parse()

	if err := checkFlags(); err != nil {
		log.Fatal(err)
	}

	if populate.FlagPopulate != "" {
		if err := populate.Populate(populate.FlagConnString != "", false); err != nil {
			log.Fatal(err)
		}
	}

	if populate.FlagPopulate != "" || populate.FlagConnString != "" {
		return
	}

	if runtime.GOOS == "darwin" {
		// TODO: that is broken
		rootDir = "/tmp/tests"
	}
	testDir := filepath.Join(rootDir, "data")
	if err := os.MkdirAll(testDir, 0644); err != nil {
		log.Fatal(err)
	}
	testProbeVid(filepath.Join(testDir, "clean.mp4"), false)
	testProbeVid(filepath.Join(testDir, "broken.mp4"), true)
}
