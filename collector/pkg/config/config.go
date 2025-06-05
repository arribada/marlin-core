// Copyright (c) The Arribada initiative.
// Licensed under the MIT License.

package config

import (
	"flag"
	"os"
	"time"

	"github.com/arribada/marlin-core/collector/pkg/controller"
	"github.com/arribada/marlin-core/collector/pkg/node/gpsr"
	"github.com/arribada/marlin-core/collector/pkg/node/videor/rtsp"
	"github.com/arribada/marlin-core/collector/pkg/node/watcher"
	"github.com/arribada/marlin-core/collector/pkg/node/watcher/gps"
)

var (
	flagDataDir string
	flagVerbose bool

	flagCaptain string

	flagGPSEnabled       bool
	flagGPSDevice        string
	flagGPSSplitDuration string

	flagVideoEnabled       bool
	flagVideoDevice        string
	flagVideoSplitDuration string

	flagWatcherEnabled bool
	flagAzureConn      string
	flagAzureShare     string
	flagUploadInterval int

	flagControllerEnabled bool
	flagControllerHost    string
	flagTranscriberURL    string
)

func init() {
	flag.StringVar(&flagDataDir, "outdir", "/perm/marlin", "output data directory")
	flag.BoolVar(&flagVerbose, "v", true, "be verbose")

	flag.StringVar(&flagCaptain, "captain", "localhost:7403", "where to reach the captain. ignore captain if empty.")

	flag.BoolVar(&flagGPSEnabled, "gps", true, "enable GPS capture")
	flag.StringVar(&flagGPSDevice, "gps_device", "/tmp/gps", "GPS capture device")
	flag.StringVar(&flagGPSSplitDuration, "gps_split", "2m", "GPS split duration")
	// TODO: RecTimeout

	flag.BoolVar(&flagVideoEnabled, "video", true, "enable Video capture")
	flag.StringVar(&flagVideoDevice, "video_device", "/tmp/gps", "Video capture device")
	flag.StringVar(&flagVideoSplitDuration, "video_split", "10m", "Video split duration")

	flag.BoolVar(&flagWatcherEnabled, "watcher", true, "enable data watcher")
	flag.StringVar(&flagAzureConn, "azure_conn", "", "Azure connection string. No uploading if empty.")
	flag.StringVar(&flagAzureShare, "azure_share", "boatymcboatface", "Azure destination share. No uploading if empty.")
	flag.IntVar(&flagUploadInterval, "upload_interval", 3600, "Interval in seconds between full upload runs.")
	// TODO: du interval

	flag.BoolVar(&flagControllerEnabled, "controller", true, "enable controller handler")
	flag.StringVar(&flagControllerHost, "controller_host", ":8360", "Host:port where we listen for controller messages.")
	flag.StringVar(&flagTranscriberURL, "transcriber_url", "http://localhost:8361", "URL where to send messages for transcriber.")
}

type Config struct {
	Captain     string
	NodeID      string
	VideorRTSP  rtsp.Config
	GPSr        gpsr.Config
	DataWatcher watcher.Config
	Controller  controller.Config
}

var Default = Config{}

func parseGPS() (*gpsr.Config, error) {
	d, err := time.ParseDuration(flagGPSSplitDuration)
	if err != nil {
		return nil, err
	}
	return &gpsr.Config{
		Enabled:       flagGPSEnabled,
		Verbose:       flagVerbose,
		DataDir:       flagDataDir,
		CaptureDevice: flagGPSDevice,
		SplitDuration: d,
		BaudRate:      9600,
	}, nil
}

func parseVideo() (*rtsp.Config, error) {
	// env vars, if set, take priority over CLI flags
	if vidDev := os.Getenv("VIDEO_DEVICE"); vidDev != "" {
		flagVideoDevice = vidDev
	}
	d, err := time.ParseDuration(flagVideoSplitDuration)
	if err != nil {
		return nil, err
	}
	return &rtsp.Config{
		Enabled:       flagVideoEnabled,
		Verbose:       flagVerbose,
		DataDir:       flagDataDir,
		CaptureDevice: flagVideoDevice,
		SplitDuration: d,
		// Continuous recording by default.
		RecTimeout: time.Duration(-1),
	}, nil
}

func parseWatcher() (*watcher.Config, error) {
	// env vars, if set, take priority over CLI flags
	if azConn := os.Getenv("AZURE_CONN"); azConn != "" {
		flagAzureConn = azConn
	}
	if azShare := os.Getenv("AZURE_SHARE"); azShare != "" {
		flagAzureShare = azShare
	}
	w := &watcher.Config{
		Enabled:        flagGPSEnabled,
		Verbose:        flagVerbose,
		DataDir:        flagDataDir,
		AzureConn:      flagAzureConn,
		AzureShare:     flagAzureShare,
		UploadInterval: flagUploadInterval,
		DuInterval:     3600,
		DuThreshold:    0.9,
	}
	// TODO: make gps watcher optional? meh.
	cfg := gps.DefaultConfig
	cfg.DataDir = flagDataDir
	w.GPS = &cfg
	return w, nil
}

func parseController() (*controller.Config, error) {
	return &controller.Config{
		Enabled:        flagControllerEnabled,
		Verbose:        flagVerbose,
		Host:           flagControllerHost,
		TranscriberURL: flagTranscriberURL,
	}, nil
}

func Parse() (*Config, error) {
	flag.Parse()

	cfg := &Config{
		Captain: flagCaptain,
	}

	if flagGPSEnabled {
		GPSr, err := parseGPS()
		if err != nil {
			return nil, err
		}
		cfg.GPSr = *GPSr
	}

	if flagVideoEnabled {
		videor, err := parseVideo()
		if err != nil {
			return nil, err
		}
		cfg.VideorRTSP = *videor
	}

	if flagWatcherEnabled {
		watcher, err := parseWatcher()
		if err != nil {
			return nil, err
		}
		cfg.DataWatcher = *watcher
	}

	if flagControllerEnabled {
		cont, err := parseController()
		if err != nil {
			return nil, err
		}
		cfg.Controller = *cont
	}

	injectVideoAlwaysOn(cfg)

	return cfg, nil
}

func injectVideoAlwaysOn(config *Config) {
	if !config.VideorRTSP.Enabled {
		return
	}
	if !config.DataWatcher.Enabled {
		return
	}
	if config.VideorRTSP.RecTimeout <= 0 {
		config.DataWatcher.VideoAlwaysOn = true
	}
}
