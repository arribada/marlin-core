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

var logger = log.New(os.Stderr, "Video: ", log.Default().Flags()|log.Lmsgprefix)

var (
	statusMu sync.RWMutex
	status   bool
)

func GetStatus() bool {
	statusMu.RLock()
	defer statusMu.RUnlock()
	return status
}

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
		vr.Forever = true
	}

	return vr, nil
}

type Videor struct {
	config Config

	// if config.RecTimeout not positive, we record forever. And we ignore button
	// presses. And we restart recording on critical errors.
	Forever bool

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
	logger.Printf("recorder starting")

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

	return v.run(ctx)
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
		logger.Printf("WARNING: found multiple capture devices (%v), and using first one found (%v)", hosts, hosts[0])
	} else {
		logger.Printf("Using device found at %s for video recording", hosts[0])
	}

	return strings.Replace(captureDevice,
		fmt.Sprintf("%s%s:%d", networkPrefix, ipPlaceHolder, defaultRTSPPort),
		fmt.Sprintf("%s:%d", hosts[0], defaultRTSPPort), 1), nil
}

// needsFinding returns whether the IP in the captureDevice string
// ends with ".N", which is the cue that we need to do brute-force
// finding.
func needsFinding(captureDevice string) (bool, string, error) {
	// TODO: see if we want to make that work for public streams (for testing)
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

func (v *Videor) handleRequest(start bool) {
	logger.Printf("handling external request")

	// !start == stop
	if !start {
		if v.Forever {
			// ignoring stop requests
			return
		}
		v.recordStopMu.RLock()
		defer v.recordStopMu.RUnlock()
		if v.recordStop == nil {
			return
		}
		logger.Printf("stopping the recording")
		v.recordStop()
		return
	}

	select {
	case v.recordStart <- struct{}{}:
		logger.Printf("starting the recording")
	default:
		logger.Printf("discarding request, already recording")
	}
}

func (v *Videor) run(ctx context.Context) error {
	logger.Printf("starting to listen for events")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errC := make(chan error)
	go func() {
		err := v.recording(ctx)
		select {
		case errC <- err:
			return
		case <-time.After(2 * time.Second):
			logger.Printf("timeout on error surfacing for: %v", err)
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case req := <-v.ctrlCh:
				v.handleRequest(req)
			}
		}
	}()

	go func() {
		if !v.Forever {
			return
		}
		logger.Printf("Starting perpetual video recording")

		// we are technically racing with recordLoop startup, so let's give it a second
		time.Sleep(time.Second)
		v.handleRequest(true)
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errC:
		return err
	}
}

func (v *Videor) recording(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-v.recordStart:
		}
		v.startTime = time.Now().UTC()
		recordCtx, cancel := context.WithCancel(ctx)
		v.recordStopMu.Lock()
		v.recordStop = cancel
		v.recordStopMu.Unlock()

		statusMu.Lock()
		status = true
		statusMu.Unlock()
		err := v.record(recordCtx)
		cancel()
		logger.Printf("recording sequence over")
		statusMu.Lock()
		status = false
		statusMu.Unlock()
		if err != nil {
			return err
		}
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

	if !vr.Forever {
		logger.Printf("starting to record for %v", vr.config.RecTimeout)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
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
		vr.readPacket(ctx, pkt)
	})

	// start playing
	_, err = vr.cl.Play(nil)
	if err != nil {
		return fmt.Errorf("while starting stream: %v", err)
	}

	// we're doing our own sync with vr.errC, because I think the Close
	// VS Wait mechanism from the lib is buggy.
	select {
	case <-ctx.Done():
		return nil
	case err = <-vr.errC:
		return err
	}
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
		OnPacketsLost: func(lost uint64) {
			// TODO: maybe later we do better, and rate limit the spam or something.
			// But for now we have this function to completely kill the "RTP packets lost" log spam.
		},
	}

	// start the stream
	if err := c.Start(); err != nil {
		return fmt.Errorf("while starting stream playing: %v", err)
	}

	// find available medias
	// give it a few tries, because sometimes connecting to the cam
	// over the network takes a couple of retries.
	// And we don't want to surface that error, in case we're not in
	// forever mode (in which case, there would not be any retry triggered
	// from the top).
	var desc *description.Session
	pause := time.Second
	for i := 0; i < 10; i++ {
		desc, _, err = c.Describe(u)
		if err == nil {
			break
		}
		time.Sleep(pause)
		pause *= 2
	}
	if err != nil {
		return fmt.Errorf("while describing stream: %v", err)
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
	cmd := exec.Command(FFmpgebin, "-i", input, "-c", "copy", output)
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
		logger.Printf("(intermediate) recording saved: %v", tsFile)
	}

	if !concurrent {
		if err := vr.writeMP4(tsFile); err != nil {
			return fmt.Errorf("while writing MP4: %v", err)
		}
		return nil
	}

	// TODO: remove concurrent mode, when we stop relying on ffmpeg.
	// Or add a chan, to surface delayed error to the caller.
	go func() {
		if err := vr.writeMP4(tsFile); err != nil {
			logger.Printf("while writing MP4: %v", err)
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
		logger.Printf("recording saved: %v", newName)
	}

	return nil
}

func (vr *Videor) splitDuration() time.Duration {
	// Split and save the whole recording into separate files.
	// Respecting the total Rec timeout.
	if vr.Forever {
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

// TODO: a map instead. anyway, make a struct and package for that and the above gross exceptions.
var lastNonSuppressed time.Time

// TODO: actually return an error? I think as long as we're doing
// mpeg-ts, it's harmless to skip some packets, and keep going with
// the next ones.
func (vr *Videor) handlePacket(pkt *rtp.Packet) {
	// decode timestamp
	pts, ok := vr.cl.PacketPTS(vr.media, pkt)
	if !ok {
		if vr.config.Verbose {
			if lastNonSuppressed.IsZero() || time.Since(lastNonSuppressed) > time.Second {
				logger.Printf("rtp: waiting for timestamp in packet")
				lastNonSuppressed = time.Now()
			}
		}
		return
	}

	// extract access unit from RTP packets
	au, err := vr.rtpDec.Decode(pkt)
	if err != nil {
		if errors.Is(err, rtph264.ErrNonStartingPacketAndNoPrevious) || errors.Is(err, rtph264.ErrMorePacketsNeeded) {
			// ignore/suppress these errors
			return
		}
		// ignore more errors
		if strings.HasPrefix(err.Error(), invalidFUA) {
			return
		}
		if strings.HasPrefix(err.Error(), discardingFrame) {
			return
		}
		if vr.config.Verbose {
			logger.Printf("rtp decoding error: %v", err)
		}
		return
	}

	// encode the access unit into MPEG-TS
	if err := vr.muxer.writeH264(au, pts); err != nil {
		// TODO: do better, i.e. something smart that only suppresses duplicate messages
		if strings.HasPrefix(err.Error(), reorderedFrames) {
			return
		}
		if vr.config.Verbose {
			logger.Printf("while writing H264 stream: %v", err)
		}
		return
	}
}

func (vr *Videor) handlePackets(ctx context.Context) (fErr error) {
	splitTimer := time.NewTimer(vr.config.SplitDuration)
	pktTimer := time.NewTimer(pktTimeout)
	for {
		select {
		case <-ctx.Done():
			logger.Printf("stopped recording, by context interruption")
			if err := vr.writeVideoFile(false); err != nil {
				return fmt.Errorf("while writing last video file: %v", err)
			}
			return nil
		case pkt := <-vr.packetC:
			// N.B: being explicit here about the fact that we ignore errors in handlePacket()
			vr.handlePacket(pkt)
			pktTimer.Reset(pktTimeout)
		case <-pktTimer.C:
			return fmt.Errorf("timeout waiting for packet")
		case <-splitTimer.C:
			if err := vr.writeVideoFile(true); err != nil {
				return fmt.Errorf("while writing video file: %v", err)
			}

			dur := vr.splitDuration()
			if dur <= 0 {
				if vr.config.Verbose {
					logger.Printf("recording reached maximum duration")
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

func (vr *Videor) readPacket(ctx context.Context, pkt *rtp.Packet) {
	select {
	case <-ctx.Done():
	case vr.packetC <- pkt:
	}
}
