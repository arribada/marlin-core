// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package config

import (
	"flag"
	"os"
	"strings"
	"time"

	"github.com/arribada/marlin-core/collector/pkg/controller"
	"github.com/arribada/marlin-core/collector/pkg/gpsr"
	"github.com/arribada/marlin-core/collector/pkg/lpm"
	"github.com/arribada/marlin-core/collector/pkg/videor/rtsp"
	"github.com/arribada/marlin-core/collector/pkg/watcher"
)

var (
	flagDataDir string
	flagVerbose bool

	flagCaptain   string
	flagReportURL string

	flagGPSEnabled       bool
	flagGPSDevice        string
	flagGPSSplitDuration string

	flagVideoEnabled       bool
	flagVideoDevice        string
	flagVideoSplitDuration string
	flagVideoRecDuration   string

	flagWatcherEnabled bool
	flagAzureConn      string
	flagAzureShare     string
	flagUploadInterval int

	flagControllerEnabled bool
	flagHost              string
	flagTranscriberURL    string

	flagSatEnabled       bool
	flagSatDevice        string
	flagSatID            string
	flagSatAddr          string
	flagSatSecKey        string
	flagSatRconf         string
	flagSatDeviceID      string
	flagCellularStatus   string
	flagTSStatus         string
	flagWifiStatus       string
	flagControllerStatus string
)

func init() {
	flag.StringVar(&flagDataDir, "datadir", "/perm/marlin", "output data directory")
	flag.BoolVar(&flagVerbose, "v", true, "be verbose")

	flag.StringVar(&flagCaptain, "captain", "localhost:7143", "where to reach the captain. ignore captain if empty.")
	flag.StringVar(&flagReportURL, "report_url", "http://localhost:7144?report=last", "where to get the report from the databrowser.")

	flag.BoolVar(&flagGPSEnabled, "gps", true, "enable GPS capture")
	flag.StringVar(&flagGPSDevice, "gps_device", "", "GPS capture device")
	flag.StringVar(&flagGPSSplitDuration, "gps_split", "2m", "GPS split duration")
	// TODO: RecTimeout

	flag.BoolVar(&flagVideoEnabled, "video", true, "enable Video capture")
	flag.StringVar(&flagVideoDevice, "video_device", "", "Video capture device. Overriden by VIDEO_DEVICE env var.")
	flag.StringVar(&flagVideoSplitDuration, "video_split", "10m", "Video split duration")
	flag.StringVar(&flagVideoRecDuration, "video_max", "-1s", "How long to record video for. Negative value means forever.")

	flag.BoolVar(&flagWatcherEnabled, "watcher", true, "enable data watcher")
	flag.StringVar(&flagAzureConn, "azure_conn", "", "Azure connection string. No uploading if empty.")
	flag.StringVar(&flagAzureShare, "azure_share", "boatymcboatface", "Azure destination share. No uploading if empty.")
	flag.IntVar(&flagUploadInterval, "upload_interval", 300, "Interval in seconds between full upload runs.")
	// TODO: du interval

	flag.BoolVar(&flagControllerEnabled, "controller", true, "enable controller handler")
	flag.StringVar(&flagHost, "host", ":7140", "Host:port where we listen for controller messages.")
	flag.StringVar(&flagTranscriberURL, "transcriber_url", "http://localhost:7141", "URL where to send messages for transcriber.")

	flag.BoolVar(&flagSatEnabled, "sat", true, "enable Satellite comms")
	flag.StringVar(&flagSatDevice, "sat_device", "", "Path to the lpm device. Typically a feather SMD wing on a serial USB port.")
	flag.StringVar(&flagSatID, "sat_id", "", "")
	flag.StringVar(&flagSatAddr, "sat_address", "", "")
	flag.StringVar(&flagSatSecKey, "sat_seckey", "", "")
	flag.StringVar(&flagSatRconf, "sat_rconf", "2c93600d6be3bac0ccfe9047c02c058e", "Satellite message size. Defaults to LDA2 (192 bits).")
	flag.StringVar(&flagSatDeviceID, "sat_device_id", "2FE3:0004", "To help automatically find sat_device, if not specified.")
	flag.StringVar(&flagCellularStatus, "cell_status_url", "http://localhost:3399/status", "Where to probe for cellular status.")
	flag.StringVar(&flagTSStatus, "ts_status_url", "", "Where to probe for TS status (where GUS at actually)")
	flag.StringVar(&flagWifiStatus, "wifi_status_url", "http://localhost:2848/status", "Where to probe for wifi status.")
	flag.StringVar(&flagControllerStatus, "controller_status_url", "", "controller status URL")
}

type Config struct {
	Captain     string
	ReportURL   string
	NodeID      string
	VideorRTSP  rtsp.Config
	GPSr        gpsr.Config
	DataWatcher watcher.Config
	Controller  controller.Config
	Sat         lpm.Config
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
		SatDeviceID:   flagSatDeviceID,
	}, nil
}

func parseVideo() (*rtsp.Config, error) {
	// env vars, if set, take priority over CLI flags
	if vidDev := os.Getenv("VIDEO_DEVICE"); vidDev != "" {
		flagVideoDevice = vidDev
	}
	split, err := time.ParseDuration(flagVideoSplitDuration)
	if err != nil {
		return nil, err
	}
	rec, err := time.ParseDuration(flagVideoRecDuration)
	if err != nil {
		return nil, err
	}
	return &rtsp.Config{
		Enabled:       flagVideoEnabled,
		Verbose:       flagVerbose,
		DataDir:       flagDataDir,
		CaptureDevice: flagVideoDevice,
		SplitDuration: split,
		// Continuous recording by default.
		RecTimeout: rec,
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
	return w, nil
}

func parseController() (*controller.Config, error) {
	return &controller.Config{
		Enabled:        flagControllerEnabled,
		Verbose:        flagVerbose,
		Host:           flagHost,
		TranscriberURL: flagTranscriberURL,
	}, nil
}

func parseSat() (*lpm.Config, error) {
	hostName, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	var controllerStatusURL string
	if flagControllerEnabled {
		// TODO: implement actual endpoint for controller to report its status.
		// we don't want to actually fetch audio from the controller, so we just ping that URL for now.
		controllerStatusURL = flagControllerStatus
	}
	var dataBrowserURL string
	if flagReportURL != "" {
		// TODO: gross, but works for now. We don't want to actually probe
		// the report endpoint as it's a bit more CPU intensive on the
		// databrowser, than fetching a static asset like trip.html
		dataBrowserURL = strings.TrimSuffix(flagReportURL, "?report=last") + "/trip.html"
	}
	// TODO: env vars
	return &lpm.Config{
		Enabled:  flagSatEnabled,
		Device:   flagSatDevice,
		Verbose:  flagVerbose,
		ID:       flagSatID,
		Addr:     flagSatAddr,
		SecKey:   flagSatSecKey,
		Rconf:    flagSatRconf,
		DeviceID: flagSatDeviceID,
		// TODO: not hardcoded. Find a way to share with ap? read it directly
		// from /etc/hostapd.conf? get in real time with some wifi tools/code?
		WifiNetwork:         "marlin",
		Hostname:            hostName,
		VideoEnabled:        flagVideoEnabled,
		GPSEnabled:          flagGPSEnabled,
		ControllerStatusURL: controllerStatusURL,
		CellularStatusURL:   flagCellularStatus,
		TSStatusURL:         flagTSStatus,
		WifiStatusURL:       flagWifiStatus,
		DataBrowserURL:      dataBrowserURL,
	}, nil
}

func Parse() (*Config, error) {
	flag.Parse()

	cfg := &Config{
		Captain:   flagCaptain,
		ReportURL: flagReportURL,
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

	if flagSatEnabled {
		sat, err := parseSat()
		if err != nil {
			return nil, err
		}
		cfg.Sat = *sat
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
