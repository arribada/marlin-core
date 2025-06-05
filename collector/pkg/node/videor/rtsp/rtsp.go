// Copyright (c) The Arribada initiative.
// Licensed under the MIT License.

package rtsp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/arribada/marlin-core/collector/pkg/logger"
	"github.com/arribada/marlin-core/common/pkg/datatype"
	"github.com/arribada/marlin-core/common/pkg/utils"

	klog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/nareix/joy5/av"
	"github.com/nareix/joy5/format"
	"github.com/nareix/joy5/format/mp4"
	"github.com/nareix/joy5/format/rtsp"
)

const (
	ComponentName   = "rtsp video recorder"
	defaultRTSPPort = 554
	ipPlaceHolder   = ".N"
)

type Config struct {
	CaptureDevice string
	DataDir       string
	// TODO: use Verbose instead of klogger
	Verbose bool
	Enabled bool
	// Maximum duration of a recording sequence. Use non positive value for continuous
	// recording.
	RecTimeout    time.Duration
	SplitDuration time.Duration
	RecAuto       bool
}

func New(lgr klog.Logger, cfg Config, ctrlCh chan bool) (*Videor, error) {
	lgr, err := logger.ApplyFilter("info", lgr)
	if err != nil {
		return nil, err
	}

	vr := &Videor{
		klogger: klog.With(lgr, "component", ComponentName),
		config:  cfg,
		ctrlCh:  ctrlCh,
		// unbuffered so that we drop any extra "start" events
		// that come in during a recording.
		recordStart: make(chan struct{}),
	}
	if cfg.RecTimeout <= 0 {
		vr.forever = true
	}

	return vr, nil
}

type Videor struct {
	klogger klog.Logger
	config  Config

	// if config.RecTimeout not positive, we record forever. And we ignore button
	// presses.
	forever bool

	// receives "button presses" events.
	// we can afford for it to be unbuffered (easier to reason with), because we're
	// processing events in a tight loop, and start recording requests get dropped
	// immediately if we're already recording.
	// true means start the video, false means stop.
	ctrlCh chan bool

	startTime   time.Time
	recordStart chan struct{}

	recordStopMu sync.RWMutex
	recordStop   context.CancelFunc

	// the actual capture device URL used. Defaults to config.CaptureDevice if it's
	// not empty. Otherwise dynamically set on startup to whatever device is
	// brute-force found on the network.
	captureDevice string
}

func (v *Videor) Run(ctx context.Context) error {
	_ = level.Info(v.klogger).Log("msg", "starting")

	if err := os.MkdirAll(v.config.DataDir, os.ModePerm); err != nil {
		return fmt.Errorf("while creating root destination dir %v: %w", v.config.DataDir, err)
	}

	if v.config.CaptureDevice == "" {
		return errors.New("no capture device in configuration")
	}

	dev, err := findCaptureDevice(v.config.CaptureDevice)
	if err != nil {
		return fmt.Errorf("while trying to probe for a capture device: %v", err)
	}
	v.captureDevice = dev

	v.startListen(ctx)

	<-ctx.Done()
	return nil
}

func findCaptureDevice(captureDevice string) (string, error) {
	nf, networkPrefix, err := needsFinding(captureDevice)
	if err != nil {
		return "", err
	}
	if !nf {
		return captureDevice, err
	}

	localAddr, err := getFrom(networkPrefix)
	if err != nil {
		return "", fmt.Errorf("while getting local addr of interface: %v", err)
	}

	parts := strings.Split(localAddr, ".")
	if len(parts) != 4 {
		return "", fmt.Errorf("unexpected form for local address: %v", localAddr)
	}
	prefix := strings.Join(parts[0:3], ".")
	suffix := 1

	timeout := time.Second
	var wg sync.WaitGroup

	var foundMu sync.RWMutex
	var hosts []string

	for {
		if suffix > 254 {
			break
		}

		// spread all the goroutine startups over 1s ~= 5ms * 254
		time.Sleep(5 * time.Millisecond)
		wg.Add(1)
		go func(sfx int) {
			defer wg.Done()
			ip := fmt.Sprintf("%s.%d", prefix, sfx)
			dest := fmt.Sprintf("%s:%d", ip, defaultRTSPPort)
			if err := tcpPing(localAddr, dest, timeout); err != nil {
				return
			}
			foundMu.Lock()
			hosts = append(hosts, ip)
			foundMu.Unlock()
		}(suffix)
		suffix++
	}

	wg.Wait()

	if len(hosts) < 1 {
		return "", errors.New("no host found for camera")
	}

	if len(hosts) > 1 {
		// TODO: do better. (try getting feed from it?)
		log.Printf("WARNING: found multiple capture devices (%v), and using first one found (%v)", hosts, hosts[0])
	} else {
		log.Printf("Using device found at %s for video recording", hosts[0])
	}

	return strings.Replace(captureDevice,
		fmt.Sprintf("%s%s:%d", networkPrefix, ipPlaceHolder, defaultRTSPPort),
		fmt.Sprintf("%s:%d", hosts[0], defaultRTSPPort), 1), nil
}

// needsFinding returns whether the IP in the captureDevice string
// ends with ".N", which is the cue that we need to do brute-force
// finding.
func needsFinding(captureDevice string) (bool, string, error) {
	parts := strings.SplitN(captureDevice, "@", 2)
	if len(parts) != 2 {
		return false, "", errors.New("no @ found in captureDevice")
	}
	portPart := fmt.Sprintf(":%d", defaultRTSPPort)
	parts = strings.SplitN(parts[1], portPart, 2)
	if len(parts) != 2 {
		return false, "", fmt.Errorf("no %s found in captureDevice", portPart)
	}
	if !strings.HasSuffix(parts[0], ipPlaceHolder) {
		return false, "", nil
	}
	return true, strings.TrimSuffix(parts[0], ipPlaceHolder), nil
}

// TODO: this can fail if the interfaces are not fully up yet. probably solved by WaitFor("net-online").
func getFrom(camNetworkPrefix string) (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	if len(addrs) < 1 {
		return "", errors.New("no net interface found")
	}
	// find the interesting one
	for _, v := range addrs {
		asString := v.String()
		if !strings.HasPrefix(asString, camNetworkPrefix) {
			continue
		}

		parts := strings.Split(asString, "/")
		if len(parts) != 2 {
			return "", fmt.Errorf("no /separator in %v", asString)
		}
		if parts[1] != "24" {
			continue
		}
		return parts[0], nil
	}
	return "", errors.New("local interface address not found")
}

func tcpPing(reqOrigin, dest string, timeout time.Duration) error {
	d := net.Dialer{
		Timeout: timeout,
		LocalAddr: &net.TCPAddr{
			IP: net.ParseIP(reqOrigin),
		},
	}

	conn, err := d.Dial("tcp", dest)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

func (v *Videor) Stop(cancel context.CancelFunc) {
	_ = level.Info(v.klogger).Log("msg", "stopped")
	cancel()
}

func (v *Videor) handleRequest(start bool) {
	_ = level.Debug(v.klogger).Log("msg", "handling external request")

	// !start == stop
	if !start {
		if v.forever {
			// ignoring stop requests
			return
		}
		v.recordStopMu.RLock()
		defer v.recordStopMu.RUnlock()
		if v.recordStop == nil {
			return
		}
		_ = level.Debug(v.klogger).Log("msg", "stopping the recording")
		v.recordStop()
		return
	}

	select {
	case v.recordStart <- struct{}{}:
		_ = level.Debug(v.klogger).Log("msg", "starting the recording")
	default:
		_ = level.Debug(v.klogger).Log("msg", "discarding request, already recording")
	}
}

func (v *Videor) startListen(ctx context.Context) {
	_ = level.Info(v.klogger).Log("msg", "starting to listen for events")
	go v.recordLoop(ctx)

	go func() {
		for {
			select {
			case <-ctx.Done():
				_ = level.Debug(v.klogger).Log("msg", "listening event loop terminated")
				return
			case req := <-v.ctrlCh:
				v.handleRequest(req)
			}
		}
	}()

	if !(v.config.RecAuto || v.forever) {
		return
	}

	if v.config.RecAuto {
		logger.LogAndSend("Starting automatic video recording")
	}
	if v.forever {
		logger.LogAndSend("Starting perpetual video recording")
	}

	// we are technically racing with recordLoop startup, so let's give it a second
	time.Sleep(time.Second)
	v.handleRequest(true)
}

func (v *Videor) recordLoop(ctx context.Context) {
	autoRestartBackoff := time.Second
	maxBackoff := 300 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-v.recordStart:
		}
		v.startTime = time.Now()
		v.recordStopMu.RLock()
		if v.recordStop != nil {
			// called here, to free the previous recording context, in case the recording
			// terminated by itself (by duration)
			v.recordStop()
		}
		v.recordStopMu.RUnlock()

		// making a child context because we now want a dedicated cancel func as a means
		// of stopping the recording.
		recordCtx, cancel := context.WithCancel(ctx)
		v.recordStopMu.Lock()
		v.recordStop = cancel
		v.recordStopMu.Unlock()

		err := v.record(recordCtx)
		_ = level.Debug(v.klogger).Log("msg", "recording sequence over")
		if err == nil {
			autoRestartBackoff = time.Second
			continue
		}

		// TODO: terminate if it's a critical error, like no storage space anymore?
		// And use an err chan to propagate the error upwards maybe?
		logger.LogAndSend(fmt.Sprintf("Video recording error: %v", err), true)

		if !v.forever {
			continue
		}
		// we auto-restart ourselves if we're in forever mode
		go func() {
			time.Sleep(autoRestartBackoff)
			autoRestartBackoff *= 2
			if autoRestartBackoff > maxBackoff {
				autoRestartBackoff = maxBackoff
			}
			select {
			case v.recordStart <- struct{}{}:
				logger.LogAndSend("Auto-restarting video recording")
			case <-time.After(5 * time.Second):
				logger.LogAndSend("Failed to auto-restart video recording")
			}
		}()
	}
}

func (v *Videor) record(ctx context.Context) (err error) {
	rtspTimeout := 3 * time.Second
	_ = level.Debug(v.klogger).Log("dialing", v.captureDevice)
	cl, err := rtsp.DialTimeout(v.captureDevice, rtspTimeout)
	if err != nil {
		return fmt.Errorf("while connecting to the capture device: %v", err)
	}
	defer func() {
		if closeErr := cl.Close(); closeErr != nil {
			err = utils.MaybeWrap(closeErr, err, "while closing capture client")
		}
	}()
	cl.RtspTimeout = rtspTimeout              // add timeout for receiving rtsp packets
	cl.RtpKeepAliveTimeout = 10 * time.Second // add keep alive rtsp packets

	if !v.forever {
		_ = level.Info(v.klogger).Log("msg", fmt.Sprintf("starting to record for %v", v.config.RecTimeout))
	}

	for {
		// Split and save the whole recording into separate files.
		// Respecting the total Rec timeout.
		var remainingDuration time.Duration
		if v.forever {
			// duration <= 0 in config means we record forever.
			remainingDuration = time.Duration(math.MaxInt64)
		} else {
			remainingDuration = v.config.RecTimeout - time.Since(v.startTime)
			if remainingDuration <= 0 {
				_ = level.Debug(v.klogger).Log("msg", "recording reached maximum duration")
				return nil
			}
		}

		nextFileDuration := v.config.SplitDuration
		if remainingDuration < v.config.SplitDuration {
			nextFileDuration = remainingDuration
		}

		if err := v.recordWithDuration(ctx, cl, nextFileDuration); err != nil {
			return fmt.Errorf("while recording: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = level.Debug(v.klogger).Log("msg", "recording sequence stopped")
			return nil
		default:
		}
	}
}

func (v *Videor) recordWithDuration(ctx context.Context, cl *rtsp.Client, timeout time.Duration) (err error) {
	// write in a temp dir, because we want to finish with a rename in the fsnotify
	// watched dir.
	tmpDir := filepath.Join(v.config.DataDir, datatype.TmpDirName)
	if err := os.MkdirAll(tmpDir, os.ModePerm); err != nil {
		return fmt.Errorf("while creating temp destination dir: %w", err)
	}
	of, err := datatype.CreateFile(tmpDir, datatype.Video)
	if err != nil {
		return fmt.Errorf("while creating record file: %w", err)
	}

	defer func() {
		err = v.finalize(err, of)
	}()

	if !v.forever {
		_ = level.Info(v.klogger).Log("msg", fmt.Sprintf("starting new recording for %v", timeout))
	}
	tm := time.NewTimer(timeout)

	var m *mp4.Muxer
	m, err = mp4.NewMuxer(format.NewStreamsWriteSeeker(of, cl))
	if err != nil {
		return fmt.Errorf("while initializing video muxer: %w", err)
	}

	if err := m.WriteFileHeader(); err != nil {
		return fmt.Errorf("while writing file header: %w", err)
	}

	success := false
	for {
		select {
		case <-ctx.Done():
			_ = level.Debug(v.klogger).Log("msg", "stopped recording from context")
			break
		case <-tm.C:
			_ = level.Debug(v.klogger).Log("msg", "recording part reached maximum size")
			break
		default:
			var pkt av.Packet
			ch := make(chan struct{})
			go func() {
				pkt, err = cl.ReadPacket()
				<-ch
			}()
			select {
			case ch <- struct{}{}:
			case <-time.After(5 * time.Second):
				close(ch)
				return errors.New("timeout on receiving packet from camera")
			}
			if err != nil {
				// we are losing some packets from camera here
				_ = level.Error(v.klogger).Log("msg", "while reading from capture device", "err", err)
				time.Sleep(100 * time.Millisecond) // Rate limit after an error to allow time to recover.
				continue
			}
			if err = m.WritePacket(pkt); err != nil {
				_ = level.Error(v.klogger).Log("msg", "while writing video packet", "err", err)
				time.Sleep(100 * time.Millisecond) // Rate limit after an error to allow time to recover.
				// overkill for now, but keep it in case we ever had anything after
				continue
			}
			// if we wrote at least one packet, consider it a success
			success = true
			continue
		}
		break
	}

	if success {
		// disregagrd read/write errors if we were successful at least once
		err = nil
	}

	if err != nil {
		return
	}

	if err := m.WriteTrailer(); err != nil {
		return fmt.Errorf("while writing video file trailer: %w", err)
	}
	return nil
}

func (v *Videor) finalize(oldErr error, of *os.File) error {
	name := of.Name()
	// if anything went wrong, abort, and remove file
	if oldErr != nil {
		// we can ignore closing error, since we're going to remove anyway
		_ = of.Close()
		if rmErr := os.Remove(name); rmErr != nil {
			return utils.MaybeWrap(rmErr, oldErr, "while removing incomplete video file")
		}
		return oldErr
	}

	if closeErr := of.Close(); closeErr != nil {
		return utils.MaybeWrap(closeErr, oldErr, fmt.Sprintf("while closing file %v", name))
	}

	newName := filepath.Join(v.config.DataDir, filepath.Base(name))
	if rnErr := os.Rename(name, newName); rnErr != nil {
		return utils.MaybeWrap(rnErr, oldErr, fmt.Sprintf("while renaming file %v", name))
	}
	_ = level.Info(v.klogger).Log("msg", "recording saved", "path", newName)

	return oldErr
}
