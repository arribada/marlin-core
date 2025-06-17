package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/arribada/marlin-core/collector/pkg/node/watcher"
)

var rootDir = "/perm/tests/"

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

func main() {
	if runtime.GOOS == "darwin" {
		rootDir = "/tmp/tests"
	}
	testDir := filepath.Join(rootDir, "data")
	if err := os.MkdirAll(testDir, 0644); err != nil {
		log.Fatal(err)
	}
	testProbeVid(filepath.Join(testDir, "clean.mp4"), false)
	testProbeVid(filepath.Join(testDir, "broken.mp4"), true)
}
