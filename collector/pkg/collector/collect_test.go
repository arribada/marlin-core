// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package collector

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/arribada/marlin-core/collector/pkg/config"
	"github.com/arribada/marlin-core/collector/pkg/gpsr"
	"github.com/arribada/marlin-core/collector/pkg/lpm/fake"
)

// TODO: one of the end goals is to test harbour mode and shutdown from low battery?

func buildGenGPS(t *testing.T, ctx context.Context) {
	bin := filepath.Join(os.TempDir(), "bin", "gengps")
	// var stderr bytes.Buffer
	// var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/gengps")
	// cmd.Stderr = &stderr
	// cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Dir = "./../../../gengps"
	err := cmd.Run()
	// out := stdout.String() + stderr.String()
	if err != nil {
		// TODO: does not kill the test
		//		t.Fatalf("gengps error: %v: %v", err, out)
		t.Fatalf("gengps build error: %v", err)
		return
	}
}

func runGenGPS(t *testing.T, ctx context.Context) {
	bin := filepath.Join(os.TempDir(), "bin", "gengps")
	cmd := exec.CommandContext(ctx, bin)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Dir = "./../../../gengps"
	err := cmd.Run()
	if err != nil {
		t.Fatalf("gengps error: %v", err)
		return
	}
}

func buildDatabrowser(t *testing.T, ctx context.Context) {
	bin := filepath.Join(os.TempDir(), "bin", "databrowser")
	// var stderr bytes.Buffer
	// var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/databrowser")
	// cmd.Stderr = &stderr
	// cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Dir = "./../../../databrowser"
	err := cmd.Run()
	// out := stdout.String() + stderr.String()
	if err != nil {
		// TODO: does not kill the test
		//		t.Fatalf("databrowser error: %v: %v", err, out)
		t.Fatalf("databrowser build error: %v", err)
	}
}

func runDatabrowser(t *testing.T, ctx context.Context, dataDir string) {
	bin := filepath.Join(os.TempDir(), "bin", "databrowser")
	cmd := exec.CommandContext(ctx, bin,
		"-captain=", "-code=./dashboard/", "-data="+dataDir, "-userpass=foo:bar", "-host=:7144")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Dir = "./../../../databrowser"
	err := cmd.Run()
	if err != nil {
		t.Fatalf("databrowser error: %v", err)
		return
	}
}

func runFakeLPM(t *testing.T, ctx context.Context) {
	if err := fake.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCollect(t *testing.T) {
	return

	// TODO: start gengps and databrowser as binaries with go run?
	// TODO: run fakelpm as func (or binary, if not in collector)

	dataDir := filepath.Join(os.TempDir(), "marlin")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(30 * time.Second)
		cancel()
	}()

	buildGenGPS(t, ctx)
	buildDatabrowser(t, ctx)

	go runGenGPS(t, ctx)
	go runDatabrowser(t, ctx, dataDir)
	go runFakeLPM(t, ctx)
	time.Sleep(time.Second)

	cfg := config.Config{
		Captain: "",
		GPSr: gpsr.Config{
			Enabled:       true,
			Verbose:       true,
			DataDir:       dataDir,
			CaptureDevice: "/tmp/gps",
			SplitDuration: 2 * time.Minute,
			BaudRate:      9600,
			SatDeviceID:   "291886",
		},
	}
	if err := Collect(&cfg); err != nil {
		t.Fatal(err)
	}

	// TODO: shouldn't a ^C terminate the test?
}
