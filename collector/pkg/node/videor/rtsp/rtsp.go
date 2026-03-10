// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package rtsp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/arribada/marlin-common/pkg/datatype"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/pion/rtp"
)

// To easily test this package as a "program":
// /user/collect -host=:7140 -captain="" -video=true -gps=false -datadir=/tmp/marlin -v=true -video_device='rtsp://foo:bar@192.168.87.250:554/live1s1.sdp' -video_split=1m

const (
	defaultRTSPPort = 554
	ipPlaceHolder   = ".N"
)

var pktTimeout = 5 * time.Second

type Config struct {
	// e.g. on Hilook:
	// rtsp://foo:bar@192.168.87.252:554/Streaming/Channels/101/?transportmode=unicast
	// Vivotek:
	// rtsp://foo:bar@192.168.87.250:554/live1s1.sdp
	// And for automatic IP discovery:
	// rtsp://foo:bar@192.168.87.N:554/Streaming/Channels/101/?transportmode=unicast
	CaptureDevice string
	DataDir       string
	Verbose       bool
	Enabled       bool
	// Maximum duration of a recording sequence. Use non positive value for continuous
	// recording. In continuous mode, recording is automatically restarted on errors.
	RecTimeout    time.Duration
	SplitDuration time.Duration
	RecAuto       bool
}

func New(cfg Config, ctrlCh chan bool) (*Videor, error) {
	vr := &Videor{
		config: cfg,
		ctrlCh: ctrlCh,
		// unbuffered so that we drop any extra "start" events
		// that come in during a recording.
		recordStart: make(chan struct{}),

		errC:    make(chan error),
		packetC: make(chan *rtp.Packet, 100),
	}
	if cfg.RecTimeout <= 0 {
		vr.forever = true
	}

	return vr, nil
}

type Videor struct {
	config Config

	// if config.RecTimeout not positive, we record forever. And we ignore button
	// presses. And we restart recording on critical errors.
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

	// stream decoding
	cl     *gortsplib.Client
	frmt   *format.H264
	media  *description.Media
	rtpDec *rtph264.Decoder
	// mpgeg-ts writer
	muxer *mpegtsMuxer

	// to synchronize handlePackets with record
	errC chan error
	// to synchronize readPacket with handlePackets
	packetC chan *rtp.Packet
}

func (v *Videor) Run(ctx context.Context) error {
	log.Printf("Video recorder starting")

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
	log.Printf("Video recorder stopped")
	cancel()
}

func (v *Videor) handleRequest(start bool) {
	log.Printf("Video recorder: handling external request")

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
		log.Printf("Video recorder: stopping the recording")
		v.recordStop()
		return
	}

	select {
	case v.recordStart <- struct{}{}:
		log.Printf("Video recorder: starting the recording")
	default:
		log.Printf("Video recorder: discarding request, already recording")
	}
}

func (v *Videor) startListen(ctx context.Context) {
	log.Printf("Video recorder: starting to listen for events")
	go v.recordLoop(ctx)

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("Video recorder: listening event loop terminated")
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
		log.Printf("Starting automatic video recording")
	}
	if v.forever {
		log.Printf("Starting perpetual video recording")
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
		v.startTime = time.Now().UTC()
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
		log.Printf("Video recorder: recording sequence over")
		if err == nil {
			autoRestartBackoff = time.Second
			continue
		}

		// TODO: terminate if it's a critical error, like no storage space anymore?
		// And use an err chan to propagate the error upwards maybe?
		log.Printf("Video recording error: %v", err)

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
				log.Printf("Auto-restarting video recording")
			case <-time.After(5 * time.Second):
				log.Printf("Failed to auto-restart video recording")
			}
		}()
	}
}

// returns nil if recording was stopped by context, or by max duration.
// returns an error otherwise (critical error). Errors that do not
// prevent the eventual creation of a video file are not returned
// (non-critical).
func (vr *Videor) record(ctx context.Context) (err error) {
	if err := vr.connect(); err != nil {
		return fmt.Errorf("while connecting to the capture device: %v", err)
	}
	defer func() {
		vr.cl.Close()
	}()

	if !vr.forever {
		log.Printf("starting to record for %v", vr.config.RecTimeout)
	}

	go func() {
		// we don't want to do all that from within readPacket, because we
		// have no control on whether/when OnPacketRTP is actually called, and
		// we need to send on vr.errC to unblock when recording is over.
		if err := vr.handlePackets(ctx); err != nil {
			vr.errC <- err
			return
		}
		vr.errC <- nil
	}()

	vr.cl.OnPacketRTP(vr.media, vr.frmt, func(pkt *rtp.Packet) {
		vr.readPacket(pkt)
	})

	// start playing
	_, err = vr.cl.Play(nil)
	if err != nil {
		return fmt.Errorf("while starting stream: %v", err)
	}

	// we're doing our own sync with vr.errC, because I think the Close
	// VS Wait mechanism from the lib is buggy.
	err = <-vr.errC
	if err != nil {
		return fmt.Errorf("recording error: %v", err)
	}

	return nil
}

func (v *Videor) newMuxer(frmt *format.H264) (*mpegtsMuxer, error) {
	// write in a temp dir, because we want to finish with a rename in the fsnotify
	// watched dir.
	tmpDir := filepath.Join(v.config.DataDir, datatype.TmpDirName)
	if err := os.MkdirAll(tmpDir, os.ModePerm); err != nil {
		return nil, fmt.Errorf("while creating temp destination dir: %w", err)
	}
	fileName, err := datatype.NewFileName(datatype.Video, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	fileName = filepath.Join(tmpDir, fileName)
	fileName = strings.Replace(fileName, ".mp4", ".ts", 1)
	// setup H264 -> MPEG-TS muxer
	mux := &mpegtsMuxer{
		fileName: fileName,
		sps:      frmt.SPS,
		pps:      frmt.PPS,
	}
	if err := mux.initialize(); err != nil {
		return nil, err
	}
	return mux, nil
}

func (vr *Videor) connect() error {
	u, err := base.ParseURL(vr.captureDevice)
	if err != nil {
		return err
	}

	c := &gortsplib.Client{
		Scheme: u.Scheme,
		Host:   u.Host,
	}

	// connect to the server
	if err := c.Start(); err != nil {
		return err
	}

	// find available medias
	desc, _, err := c.Describe(u)
	if err != nil {
		return err
	}

	// find the H264 media and format
	var frmt *format.H264
	media := desc.FindFormat(&frmt)
	if media == nil {
		return errors.New("media not found")
	}

	// setup RTP -> H264 decoder
	rtpDec, err := frmt.CreateDecoder()
	if err != nil {
		return fmt.Errorf("failed to creade h264 decoder: %v", err)
	}

	// setup a single media
	_, err = c.Setup(desc.BaseURL, media, 0, 0)
	if err != nil {
		return fmt.Errorf("failed to setup media: %v", err)
	}

	vr.cl = c
	vr.rtpDec = rtpDec
	vr.media = media
	vr.frmt = frmt

	muxer, err := vr.newMuxer(vr.frmt)
	if err != nil {
		return fmt.Errorf("while initializing mpeg-ts muxer: %v", err)
	}
	vr.muxer = muxer

	return nil
}

// should be a const, but actually a var so we can change it during tests
var FFmpgebin string

func init() {
	// location in production on gokrazy
	FFmpgebin = "/usr/local/bin/ffmpeg"
	loc, err := exec.LookPath("ffmpeg")
	if err == nil {
		FFmpgebin = loc
		return
	}
}

func tsToMP4(input string) (string, string, error) {
	output := strings.Replace(input, ".ts", ".mp4", 1)
	var stderr bytes.Buffer
	var stdout bytes.Buffer
	cmd := exec.Command(FFmpgebin, "-i", input, output)
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	err := cmd.Run()
	out := stdout.String() + stderr.String()
	if err != nil {
		return "", "", fmt.Errorf("while converting %v to mp4: %v", input, out)
	}
	return out, output, nil
}

func (vr *Videor) writeVideoFile(concurrent bool) error {
	tsFile := vr.muxer.fileName
	// TODO: maybe make close return an error.
	vr.muxer.close()
	if vr.config.Verbose {
		log.Printf("finished writing %v", tsFile)
	}

	// TODO: remove concurrent mode, when we stop relying on ffmpeg.
	if !concurrent {
		if err := vr.writeMP4(tsFile); err != nil {
			return fmt.Errorf("while writing MP4: %v", err)
		}
		return nil
	}

	go func() {
		if err := vr.writeMP4(tsFile); err != nil {
			log.Printf("while writing MP4: %v", err)
		}
	}()

	return nil
}

func (vr *Videor) writeMP4(tsFile string) error {
	_, name, err := tsToMP4(tsFile)
	if err != nil {
		return err
	}

	if err := os.Remove(tsFile); err != nil {
		return err
	}

	newName := filepath.Join(vr.config.DataDir, filepath.Base(name))
	if err := os.Rename(name, newName); err != nil {
		return fmt.Errorf("while renaming %v: %v", name, err)
	}

	if vr.config.Verbose {
		log.Printf("finished writing %v", newName)
	}

	return nil
}

func (vr *Videor) splitDuration() time.Duration {
	// Split and save the whole recording into separate files.
	// Respecting the total Rec timeout.
	if vr.forever {
		// duration <= 0 in config means we record forever.
		return vr.config.SplitDuration
	}

	sinceStart := time.Now().UTC().Sub(vr.startTime)
	dur := vr.config.RecTimeout - sinceStart
	if dur < vr.config.SplitDuration {
		return dur
	}

	return vr.config.SplitDuration
}

const (
	invalidFUA      = "invalid FU-A packet"
	reorderedFrames = "too many reordered frames"
	discardingFrame = "discarding frame since a RTP packet is missing"
)

func (vr *Videor) handlePacket(pkt *rtp.Packet) error {
	// decode timestamp
	pts, ok := vr.cl.PacketPTS(vr.media, pkt)
	if !ok {
		if vr.config.Verbose {
			log.Printf("rtp: waiting for timestamp in packet")
		}
		return nil
	}

	// extract access unit from RTP packets
	au, err := vr.rtpDec.Decode(pkt)
	if err != nil {
		if !errors.Is(err, rtph264.ErrNonStartingPacketAndNoPrevious) && !errors.Is(err, rtph264.ErrMorePacketsNeeded) {
			if vr.config.Verbose {
				if strings.HasPrefix(err.Error(), invalidFUA) {
					return nil
				}
				if strings.HasPrefix(err.Error(), discardingFrame) {
					return nil
				}
				log.Printf("rtp decoding error: %v", err)
			}
			return nil
		}
		return nil
	}

	// encode the access unit into MPEG-TS
	if err := vr.muxer.writeH264(au, pts); err != nil {
		if vr.config.Verbose {
			// TODO: do better, i.e. something smart that only suppresses duplicate messages
			if strings.HasPrefix(err.Error(), reorderedFrames) {
				return nil
			}
			log.Printf("while writing H264 stream: %v", err)
		}
		return nil
	}

	return nil
}

func (vr *Videor) handlePackets(ctx context.Context) (fErr error) {
	splitTimer := time.NewTimer(vr.config.SplitDuration)
	pktTimer := time.NewTimer(pktTimeout)
	for {
		select {
		case <-ctx.Done():
			log.Printf("stopped recording, by context interruption")
			if err := vr.writeVideoFile(false); err != nil {
				return fmt.Errorf("while writing last video file: %v", err)
			}
			return nil
		case pkt := <-vr.packetC:
			// At the moment, from within handlePacket we actually ignore
			// all errors, so we don't even need to check for errors on return
			// here. If we ever change our minds about that, and decide to return
			// on error here, we might want to write to disk whatever video we've
			// had so far.
			vr.handlePacket(pkt)
			pktTimer.Reset(pktTimeout)
		case <-pktTimer.C:
			return fmt.Errorf("timeout waiting for packet")
		case <-splitTimer.C:
			// This error handling is a lie atm, since writeVideoFile does not
			// return any error in concurrent mode.
			if err := vr.writeVideoFile(true); err != nil {
				return fmt.Errorf("while writing video file: %v", err)
			}

			dur := vr.splitDuration()
			if dur <= 0 {
				if vr.config.Verbose {
					log.Printf("recording reached maximum duration")
				}
				return nil
			}

			// Better to do newMuxer here (rather than earlier), so in case of
			// maximum duration we do not create an empty .ts file for nothing.
			mx, err := vr.newMuxer(vr.frmt)
			if err != nil {
				return fmt.Errorf("while initializing new mpgeg-ts muxer: %v", err)
			}
			// I have checked that OnPacketRTP is called synchronously and sequentially for
			// each packet, so it is safe to reset the muxer directly in there.
			vr.muxer = mx

			splitTimer.Reset(dur)
			pktTimer.Reset(pktTimeout)
		}
	}
}

func (vr *Videor) readPacket(pkt *rtp.Packet) {
	tm := time.NewTimer(pktTimeout)
	select {
	case vr.packetC <- pkt:
	// we need this timeout for termination. Otherwise OnPacketRTP
	// deadlocks the final Close().
	case <-tm.C:
		return
	}
}
