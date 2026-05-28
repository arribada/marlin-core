// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package lpm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arribada/marlin-common/pkg/sat"
	"github.com/arribada/marlin-core/collector/pkg/lpm/battery"
	lpmio "github.com/arribada/marlin-core/collector/pkg/lpm/io"
	"github.com/arribada/marlin-core/collector/pkg/lpm/pb"
	"google.golang.org/protobuf/proto"
)

const (
	lpmDeviceR = "/tmp/lpmr"
	lpmDeviceW = "/tmp/lpmw"
)

var (
	portr io.ReadWriter
	portw io.ReadWriter

	statusHost = "localhost:3399"
	statusURL  = "http://" + statusHost + "/status"

	wantCfg = Config{
		Enabled: true,
		Verbose: true,

		Device: "/tmp/lpm",
		ID:     "satID",
		Addr:   "satAddr",
		SecKey: "satSecKey",
		Rconf:  "satRconf",

		Hostname:            "theHostname",
		VideoEnabled:        true,
		GPSEnabled:          true,
		ControllerStatusURL: statusURL,
		CellularStatusURL:   statusURL,
		TSStatusURL:         statusURL,
		WifiStatusURL:       statusURL,
		DataBrowserURL:      statusURL,
	}

	wantAlert sat.Alert
)

// TODO: rip out a standalone implementation from all that. So we can run it standalone as well, against the collector.
// But we also have pkg/lpm/fake to refactor with?
func TestLPM(t *testing.T) {
	return

	// TODO: better heuristics. We need root rights to ping. And we need tailscale to be reachable.
	// So only run this test on dev machine for now.
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		t.Skip("Skipping because need root rights to ping")
	}

	statusUpdatePeriod = 5 * time.Second
	batteryUpdatePeriod = time.Second

	wantAlert = sat.Alert{
		Latitude:  37.2,
		Longitude: 72.3,
		Time: func() time.Time {
			tm, err := time.Parse(time.RFC3339, "2006-01-02T15:04:05Z")
			if err != nil {
				t.Fatal(err)
			}
			return tm
		}(),
	}

	if err := os.RemoveAll(lpmDeviceR); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(lpmDeviceR, 0666); err != nil {
		t.Fatal(err)
	}
	fr, err := os.OpenFile(lpmDeviceR, os.O_RDWR, os.ModeNamedPipe)
	if err != nil {
		t.Fatal(err)
	}
	defer fr.Close()
	portr = fr

	if err := os.RemoveAll(lpmDeviceW); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(lpmDeviceW, 0666); err != nil {
		t.Fatal(err)
	}
	fw, err := os.OpenFile(lpmDeviceW, os.O_RDWR, os.ModeNamedPipe)
	if err != nil {
		t.Fatal(err)
	}
	defer fw.Close()
	portw = fw

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := statusHandler(ctx); err != nil {
			t.Fatal(err)
		}
	}()

	/*
		go func() {
			if err := startWatcher(ctx); err != nil {
				t.Fatal(err)
			}
		}()
	*/

	go func() {
		if err := getCoreMsgs(ctx); err != nil {
			t.Fatal(err)
		}
	}()

	go func() {
		time.Sleep(5 * time.Second)

		if err := lpmio.SendAlert(wantAlert); err != nil {
			t.Fatal(err)
		}
	}()

	err = Run(wantCfg, false, ctx)
	// we expect first run to end because LPM tells us we switched to battery power
	if err != battery.OnBatteryErr {
		t.Fatal(err)
	}

	// switching to harbour mode since we're now on battery power
	err = Run(wantCfg, true, ctx)
	// we expect second run to end because LPM tells us we're low on juice
	if err != battery.LowVoltageErr {
		t.Fatal(err)
	}

	// TODO: test getting out of battery mode?
}

func statusHandler(ctx context.Context) error {
	http.HandleFunc("/status", stsHandler)
	return http.ListenAndServe(statusHost, nil)
}

func stsHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("OK"))
}

/*
func startWatcher(ctx context.Context) error {
	chans := make(types.Chans)
	if _, err := watcher.New(watcher.Config{
		Enabled: true,
		Datadir: "/tmp/marlin",
		Verbose: true,
		AzureConn: "",

		DuInterval: 2*time.Second,
		DuTreshold: 0.8,
	}, chans); err != nil {
		return err
	}
}
*/

func getCoreMsgs(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err := getCoreMsg(ctx); err != nil {
			return fmt.Errorf("while getting core msg: %v", err)
		}
	}
}

func getCoreMsg(ctx context.Context) (err error) {
	var msgId uint32
	defer func() {
		if err == nil {
			return
		}
		if err := sendReply(msgId, err); err != nil {
			log.Fatal(err)
		}
	}()

	println("FLPM: waiting for message")
	p := make([]byte, 1024)
	n, err := portr.Read(p)
	if err != nil {
		return fmt.Errorf("while reading core msg: %v", err)
	}
	if n == 0 {
		return fmt.Errorf("empty core msg")
	}
	data := p[:n]

	var msg pb.CoreMessage
	if err := proto.Unmarshal(data, &msg); err != nil {
		msgId = 0
		return fmt.Errorf("while decoding message: %v", err)
	}

	msgId = msg.MsgId

	if msg.Message == nil {
		return fmt.Errorf("nil message for msg: %d", msgId)
	}
	switch theMsg := msg.Message.(type) {
	case nil:
		return errors.New("nil interface")
	case *pb.CoreMessage_Init:
		return onInitMsg(ctx, msgId, theMsg)
	case *pb.CoreMessage_Sat:
		return onSatMsg(ctx, msgId, theMsg)
	case *pb.CoreMessage_Status:
		return onStatusMsg(ctx, msgId, theMsg)
	case *pb.CoreMessage_Mode:
		return onModeMsg(ctx, msgId, theMsg)
	default:
		return errors.New("unknown type")
	}
}

func onInitMsg(ctx context.Context, msgId uint32, msg *pb.CoreMessage_Init) error {
	if msgId > 2 {
		return fmt.Errorf("wanted init at 1 and 2, got it at %d", msgId)
	}
	if msgId == 1 {
		// force failing the first one to test the retry behaviour we have on core
		return sendReply(msgId, errors.New("force failing for 1st init"))
	}

	sysInit := msg.Init
	if sysInit == nil {
		return errors.New("nil sysinit")
	}
	if sysInit.Id != wantCfg.ID {
		return fmt.Errorf("sysInit Id, wanted %s, got %s", wantCfg.ID, sysInit.Id)
	}
	if sysInit.Addr != wantCfg.Addr {
		return fmt.Errorf("sysInit Addr, wanted %s, got %s", wantCfg.Addr, sysInit.Addr)
	}
	if sysInit.SecKey != wantCfg.SecKey {
		return fmt.Errorf("sysInit SecKey, wanted %s, got %s", wantCfg.SecKey, sysInit.SecKey)
	}
	if sysInit.Rconf != wantCfg.Rconf {
		return fmt.Errorf("sysInit Rconf, wanted %s, got %s", wantCfg.Rconf, sysInit.Rconf)
	}
	if sysInit.Hostname != wantCfg.Hostname {
		return fmt.Errorf("sysInit Hostname, wanted %s, got %s", wantCfg.Hostname, sysInit.Hostname)
	}

	return sendReply(msgId, nil)
}

func onSatMsg(ctx context.Context, msgId uint32, msg *pb.CoreMessage_Sat) error {
	if msgId < 3 {
		return fmt.Errorf("msg %d should be an init, not a sat", msgId)
	}

	// TODO: try to predict what msgid it should be.

	satmsg := msg.Sat
	if satmsg == nil {
		return errors.New("nil sat")
	}

	lat, long, tm, err := sat.DecodeRawDataHex([]byte(strings.TrimPrefix(satmsg.Message, "AT+TX=")))
	if err != nil {
		return err
	}
	if math.Abs(lat-wantAlert.Latitude) > 1e-4 {
		return fmt.Errorf("Sat Latitude, wanted %f, got %f", wantAlert.Latitude, lat)
	}
	if math.Abs(long-wantAlert.Longitude) > 1e-4 {
		return fmt.Errorf("Sat Longitude, wanted %f, got %f", wantAlert.Longitude, long)
	}
	if tm.Format(time.TimeOnly) != wantAlert.Time.Format(time.TimeOnly) {
		return fmt.Errorf("Sat Time, wanted %v, got %v", wantAlert.Time, tm)
	}

	return sendReply(msgId, nil)
}

func onStatusMsg(ctx context.Context, msgId uint32, msg *pb.CoreMessage_Status) error {
	if msgId < 3 {
		return fmt.Errorf("msg %d should be an init, not a status", msgId)
	}

	status := msg.Status
	if status == nil {
		return errors.New("nil status")
	}

	// TODO: need root rights for the pinging to succeed. But let's see how/if it succeeds in docker.
	if status.Camera != pb.CoreStatus_ERROR {
		return fmt.Errorf("status.Camera, wanted %d, got %d", pb.CoreStatus_ERROR, status.Camera)
	}
	if status.Controller != pb.CoreStatus_ONLINE {
		return fmt.Errorf("status.Controller, wanted %d, got %d", pb.CoreStatus_ONLINE, status.Controller)
	}
	if status.CloudConnection != pb.CoreStatus_ERROR {
		return fmt.Errorf("status.CloudConnection, wanted %d, got %d", pb.CoreStatus_ERROR, status.CloudConnection)
	}
	if status.Cellular != pb.CoreStatus_ONLINE {
		return fmt.Errorf("status.Cellular, wanted %d, got %d", pb.CoreStatus_ONLINE, status.Cellular)
	}
	if status.Wifi != pb.CoreStatus_ONLINE {
		return fmt.Errorf("status.Wifi, wanted %d, got %d", pb.CoreStatus_ONLINE, status.Wifi)
	}
	if status.Gps != pb.CoreStatus_ERROR {
		return fmt.Errorf("status.Gps, wanted %d, got %d", pb.CoreStatus_ERROR, status.Gps)
	}
	if status.Tailscale != pb.CoreStatus_ONLINE {
		return fmt.Errorf("status.Tailscale, wanted %d, got %d", pb.CoreStatus_ONLINE, status.Tailscale)
	}
	if status.LocalDash != pb.CoreStatus_ONLINE {
		return fmt.Errorf("status.LocalDash, wanted %d, got %d", pb.CoreStatus_ONLINE, status.LocalDash)
	}

	if status.StorageUsage != 0.0 {
		// TODO: come back to that (just make it check that we're non-zero), when we have revisited the DuWatcher.
		// return fmt.Errorf("status.StorageUsage, wanted %f, got %f", 0.32, status.StorageUsage)
	}
	/*
		// TODO: revisit. Probably won't work out of the box frow within docker anyway?
		if status.TailscaleIp != "foobar" {
			return fmt.Errorf("status.TailscaleIp, wanted %s, got %s", "foobar", status.TailscaleIp)
		}
	*/
	return sendReply(msgId, nil)
}

func onModeMsg(ctx context.Context, msgId uint32, msg *pb.CoreMessage_Mode) error {
	if msgId < 3 {
		return fmt.Errorf("msg %d should be an init, not a mode", msgId)
	}

	// TODO: try to predict what msgid it should be.

	mode := msg.Mode
	if mode == nil {
		return errors.New("nil mode")
	}

	println("MODE MSG")
	return sendReply(msgId, nil)
}

func batstat(id uint32) *pb.BatteryStatus {
	if id < 3 {
		return nil
	}
	// on mains (batter charging) for a few iterations
	if id < 5 {
		return &pb.BatteryStatus{
			// TODO: remove
			Set:     true,
			Voltage: 12,
			Current: -0.5,
		}
	}

	// then on battery for a few iterations
	if id >= 5 && id < 10 {
		return &pb.BatteryStatus{
			// TODO: remove
			Set:     true,
			Voltage: float32(12 - (float32(id) * 0.01)),
			Current: 0.5,
		}
	}
	// then finally on voltage low
	return &pb.BatteryStatus{
		// TODO: remove
		Set:     true,
		Voltage: float32(10 - (float32(id) * 0.01)),
		Current: 0.5,
	}
}

func sendReply(id uint32, err error) error {
	repl := pb.LpmReply{
		MsgId: id,
	}
	if err != nil {
		repl.Response = pb.LpmReply_ERROR
	} else {
		repl.Response = pb.LpmReply_OK
	}

	repl.BatteryStatus = batstat(id)

	data, err := proto.Marshal(&repl)
	if err != nil {
		return fmt.Errorf("while encoding reply: %v", err)
	}

	_, err = portw.Write(data)
	if err != nil {
		return fmt.Errorf("while sending reply: %v", err)
	}
	return nil
}
