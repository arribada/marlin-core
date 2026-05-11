// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package lpm

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/arribada/marlin-core/collector/pkg/gpsr"
	lpmio "github.com/arribada/marlin-core/collector/pkg/lpm/io"
	"github.com/arribada/marlin-core/collector/pkg/lpm/pb"
	"github.com/arribada/marlin-core/collector/pkg/videor/rtsp"
	"github.com/arribada/marlin-core/collector/pkg/watcher"
	"github.com/mpl/basicauth"
	"tailscale.com/cmd/tailscale/cli"
	"tailscale.com/net/ping"
	tlogger "tailscale.com/types/logger"

	"go.bug.st/serial/enumerator"
)

var logger = log.New(os.Stderr, "LPM: ", log.Default().Flags()|log.Lmsgprefix)

type Config struct {
	Enabled bool
	Verbose bool

	// actual sat comms parameters
	Device   string
	DeviceID string
	ID       string
	Addr     string
	SecKey   string
	Rconf    string

	// extra parameters for LPM to display
	Hostname    string
	WifiNetwork string
	// a bit clunky to reinject that information in our own config,
	// whereas it's already in the top level config. But we can't really
	// depend on the top level config in this here package because of
	// circular deps.
	VideoEnabled        bool
	GPSEnabled          bool
	ControllerStatusURL string
	CellularStatusURL   string
	TSStatusURL         string
	WifiStatusURL       string
	DataBrowserURL      string
}

var statusUpdatePeriod = 30 * time.Second

// for passing around the cfg, and for caching some states
type systemState struct {
	cfg Config

	mu         sync.RWMutex
	cellStatus bool
}

func Run(cfg Config, ctx context.Context) error {
	if !cfg.Enabled {
		return nil
	}

	// lpmio.Verbose = cfg.Verbose

	if cfg.Device == "" {
		dev, err := findSerialUSBDevice(cfg.DeviceID, true)
		if err != nil {
			return fmt.Errorf("while trying to automatically find LPM device: %v", err)
		}
		cfg.Device = dev
	}

	logger.Printf("using device: %v", cfg.Device)
	port, err := lpmio.OpenPort(cfg.Device)
	if err != nil {
		return fmt.Errorf("while opening LPM as serial device: %v", err)
	}
	defer func() {
		if err := port.Close(); err != nil {
			logger.Printf("while closing serial connection: %v", err)
		}
	}()

	sysInit := pb.SysInit{
		Id:          cfg.ID,
		Addr:        cfg.Addr,
		SecKey:      cfg.SecKey,
		Rconf:       cfg.Rconf,
		Hostname:    cfg.Hostname,
		WifiNetwork: cfg.WifiNetwork,
	}
	lpmio.InitRetry(sysInit, ctx)

	go lpmio.GetReplies(ctx)

	time.Sleep(time.Second)

	ss := &systemState{
		cfg: cfg,
	}
	go ss.sendStatuses(ctx)

	<-ctx.Done()
	return nil
}

func (ss *systemState) sendStatuses(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(statusUpdatePeriod):
		}
		if err := ss.sendCoreStatus(ctx); err != nil {
			logger.Printf("while sending core status %v", err)
			continue
		}
	}
}

func (ss *systemState) sendCoreStatus(ctx context.Context) error {
	coreStatus, err := ss.coreStatus(ctx)
	if err != nil {
		return err
	}

	return lpmio.SendCoreStatus(coreStatus)
}

// TODO: I think the different pb.CoreStatus don't really work, but
// nvm, let's rethink it later.
// TODO: also rethink error handling, but again depends on what meaning we want for all of this.
func (ss *systemState) coreStatus(ctx context.Context) (pb.CoreStatus, error) {
	// the order matters, because we fail early in TS and Cloud if cell is already a fail
	cell := ss.cellularStatus()
	ts := ss.tailscaleStatus(ctx)
	cc := ss.cloudStatus(ctx)
	cont := controllerStatus(ss.cfg, ctx)

	return pb.CoreStatus{
		Camera:          videoStatus(ss.cfg),
		Controller:      cont,
		Cellular:        cell,
		Tailscale:       ts,
		CloudConnection: cc,
		Wifi:            wifiStatus(ss.cfg),
		Gps:             gpsStatus(ss.cfg),
		LocalDash:       dataBrowserStatus(ss.cfg),
		StorageUsage:    float32(watcher.GetStorageUsage()),
		TailscaleIp:     tsIP(),
	}, nil
}

func videoStatus(cfg Config) pb.CoreStatus_Status {
	if !cfg.VideoEnabled {
		// TODO: or do we want UNKNOWN in that case? the Statuses are kinda clunky anyway.
		return pb.CoreStatus_OFFLINE
	}
	st := rtsp.GetStatus()
	if st {
		return pb.CoreStatus_ONLINE
	}
	return pb.CoreStatus_ERROR
}

func gpsStatus(cfg Config) pb.CoreStatus_Status {
	if !cfg.GPSEnabled {
		// TODO: or do we want UNKNOWN in that case? the Statuses are kinda clunky anyway.
		return pb.CoreStatus_OFFLINE
	}
	st := gpsr.GetStatus()
	if st {
		return pb.CoreStatus_ONLINE
	}
	return pb.CoreStatus_ERROR
}

func controllerStatus(cfg Config, ctx context.Context) pb.CoreStatus_Status {
	if cfg.ControllerStatusURL == "" {
		return pb.CoreStatus_OFFLINE
	}

	// TODO: we ping for now, because atm there's no endpoint on the
	// controller just to report status, and we don't want to actually
	// fetch audio from it, as it might mess things up.
	// Anyway, do HTTP instead when we have a status endpoint there.
	contURL, err := url.Parse(cfg.ControllerStatusURL)
	if err != nil {
		return pb.CoreStatus_ERROR
	}
	contAddr := contURL.Hostname()

	dest, err := net.ResolveIPAddr("ip4", contAddr)
	if err != nil {
		return pb.CoreStatus_ERROR
	}
	var lg tlogger.Logf
	var lp net.ListenConfig
	p := ping.New(ctx, lg, &lp)
	defer p.Close()

	nctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, err = p.Send(nctx, dest, nil)
	cancel()
	if err != nil {
		return pb.CoreStatus_OFFLINE
	}

	return pb.CoreStatus_ONLINE
}

func (ss *systemState) cellularStatus() (val pb.CoreStatus_Status) {
	defer func() {
		ss.mu.Lock()
		ss.cellStatus = (val == pb.CoreStatus_ONLINE)
		ss.mu.Unlock()
	}()
	if ss.cfg.CellularStatusURL == "" {
		return pb.CoreStatus_OFFLINE
	}

	resp, err := http.Get(ss.cfg.CellularStatusURL)
	if err != nil {
		return pb.CoreStatus_OFFLINE
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return pb.CoreStatus_ERROR
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return pb.CoreStatus_ERROR
	}
	sdat := string(data)
	if sdat != "OK" {
		return pb.CoreStatus_ERROR
	}

	return pb.CoreStatus_ONLINE
}

func (ss *systemState) tailscaleStatus(ctx context.Context) pb.CoreStatus_Status {
	var cs bool
	ss.mu.RLock()
	cs = ss.cellStatus
	ss.mu.RUnlock()
	if !cs {
		return pb.CoreStatus_OFFLINE
	}
	if ss.cfg.TSStatusURL == "" {
		return pb.CoreStatus_OFFLINE
	}

	// TODO: investigate other methods. call "tailscale status"? use tailscale.com/health ?

	gusURL, err := url.Parse(ss.cfg.TSStatusURL)
	if err != nil {
		return pb.CoreStatus_ERROR
	}
	gusAddr := gusURL.Hostname()

	dest, err := net.ResolveIPAddr("ip4", gusAddr)
	if err != nil {
		return pb.CoreStatus_ERROR
	}
	var lg tlogger.Logf
	var lp net.ListenConfig
	p := ping.New(ctx, lg, &lp)
	defer p.Close()

	nctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, err = p.Send(nctx, dest, nil)
	cancel()

	if err != nil {
		logger.Printf("while pinging GUS over TS: %v", err)
		return pb.CoreStatus_OFFLINE
	}

	return pb.CoreStatus_ONLINE
}

func (ss *systemState) cloudStatus(ctx context.Context) pb.CoreStatus_Status {
	var cs bool
	ss.mu.RLock()
	cs = ss.cellStatus
	ss.mu.RUnlock()
	if !cs {
		return pb.CoreStatus_OFFLINE
	}

	st := watcher.GetConnectionStatus()
	if st {
		return pb.CoreStatus_ONLINE
	}
	return pb.CoreStatus_ERROR
}

func wifiStatus(cfg Config) (val pb.CoreStatus_Status) {
	if cfg.WifiStatusURL == "" {
		return pb.CoreStatus_OFFLINE
	}

	resp, err := http.Get(cfg.WifiStatusURL)
	if err != nil {
		return pb.CoreStatus_OFFLINE
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return pb.CoreStatus_ERROR
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return pb.CoreStatus_ERROR
	}
	sdat := string(data)
	if sdat != "OK" {
		return pb.CoreStatus_ERROR
	}

	return pb.CoreStatus_ONLINE
}

func dataBrowserStatus(cfg Config) (val pb.CoreStatus_Status) {
	if cfg.DataBrowserURL == "" {
		return pb.CoreStatus_OFFLINE
	}

	// NewUserPass automatically checks for AUTH_USERPASS
	up, err := basicauth.NewUserPass("")
	if err != nil {
		return pb.CoreStatus_ERROR
	}
	req, err := http.NewRequest("GET", cfg.DataBrowserURL, nil)
	if err != nil {
		return pb.CoreStatus_ERROR
	}
	req.SetBasicAuth(up.U, up.P)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return pb.CoreStatus_OFFLINE
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return pb.CoreStatus_ERROR
	}
	_, err = io.ReadAll(resp.Body)
	if err != nil {
		return pb.CoreStatus_ERROR
	}
	// TODO: actually check the contents?

	return pb.CoreStatus_ONLINE
}

func tsIP() string {
	var buf bytes.Buffer
	// TODO: something less drastic. We just want to filter the client VS server version mismatch warning.
	cli.Stderr = io.Discard
	cli.Stdout = &buf
	if err := cli.Run([]string{"ip"}); err != nil {
		logger.Printf("while getting tailscale IP: %v", err)
		return ""
	}
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		return sc.Text()
	}
	if err := sc.Err(); err != nil {
		logger.Printf("while parsing tailscale IP: %v", err)
		return ""
	}
	return ""
}

// TODO: duplicated in pkg/gpsr. refactor.
func findSerialUSBDevice(satDeviceID string, isLPM bool) (string, error) {
	if satDeviceID == "" {
		return "", errors.New("-sat_device_id not specified")
	}
	satDeviceID = strings.ToLower(satDeviceID)
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return "", fmt.Errorf("while enumerating USB serial ports: %v", err)
	}
	if len(ports) == 0 {
		return "", fmt.Errorf("no serial USB port found")
	}
	var wantPort string
	if runtime.GOOS == "darwin" {
		wantPort = "/dev/cu.usbmodem"
	} else {
		wantPort = "/dev/ttyACM"
	}
	for _, port := range ports {
		if !strings.HasPrefix(port.Name, wantPort) {
			continue
		}
		id := strings.ToLower(fmt.Sprintf("%s:%s", port.VID, port.PID))
		if isLPM {
			if id == satDeviceID {
				return port.Name, nil
			}
		} else {
			if id != satDeviceID {
				return port.Name, nil
			}
		}
	}
	return "", errors.New("no suitable device found")
}
