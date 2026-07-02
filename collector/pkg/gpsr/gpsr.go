// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package gpsr

import (
	"bufio"
	context "context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/arribada/marlin-common/pkg/datatype"
	"github.com/arribada/marlin-common/pkg/utils"
	"github.com/arribada/marlin-core/collector/pkg/types"

	"github.com/adrianmo/go-nmea"
	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

var logger = log.New(os.Stderr, "GPS: ", log.Default().Flags()|log.Lmsgprefix)

var (
	statusMu sync.RWMutex
	status   bool
)

func GetStatus() bool {
	statusMu.RLock()
	defer statusMu.RUnlock()
	return status
}

type Config struct {
	CaptureDevice string
	DataDir       string
	Verbose       bool
	Enabled       bool
	SplitDuration time.Duration
	BaudRate      int
	KeepInvalid   bool // for debugging
	SatDeviceID   string
}

type GPSr struct {
	config Config

	fakeDevice bool // for synthetic data
	// dilution of precision. should be < 0 until initialized by the first GGA
	// message we get.
	dop float64
	// TODO: it would be nice if we didn't have to store HDOP for each RMC, since it
	// probably does not vary often. We could have a top struct that is one HDOP, and a
	// slice of SRMCs, and we make a new one everytime the HDOP changes, but that
	// sounds awkward...
	rmc []datatype.SRMCplus

	latestPosMu sync.RWMutex
	latestPos   types.Position
	posChan     chan types.Position
}

func trimRMC(rmc nmea.RMC) datatype.SRMC {
	return datatype.SRMC{
		Raw:       rmc.BaseSentence.Raw,
		Time:      rmc.Time,
		Validity:  rmc.Validity,
		Latitude:  rmc.Latitude,
		Longitude: rmc.Longitude,
		Speed:     rmc.Speed,
		Course:    rmc.Course,
		Date:      rmc.Date,
		Variation: rmc.Variation,
		FFAMode:   rmc.FFAMode,
		NavStatus: rmc.NavStatus,
	}
}

func New(cfg Config, posChan chan types.Position) (*GPSr, error) {
	if cfg.CaptureDevice == "" {
		dev, err := findSerialUSBDevice(cfg.SatDeviceID, false)
		if err != nil {
			return nil, fmt.Errorf("while trying to automatically find GPS device: %v", err)
		}
		cfg.CaptureDevice = dev
	}
	logger.Printf("capture device: %v", cfg.CaptureDevice)
	fakeDevice := false
	if !strings.HasPrefix(cfg.CaptureDevice, "/dev") {
		fakeDevice = true
	}

	return &GPSr{
		fakeDevice: fakeDevice,
		config:     cfg,
		dop:        -1,
		posChan:    posChan,
	}, nil
}

func (g *GPSr) Run(ctx context.Context) error {
	logger.Printf("recorder starting")

	if err := os.MkdirAll(g.config.DataDir, os.ModePerm); err != nil {
		return fmt.Errorf("while creating root destination dir %v: %w", g.config.DataDir, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go g.listenPosChan(ctx)

	return g.recording(ctx)
}

func (g *GPSr) listenPosChan(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.posChan:
			g.latestPosMu.RLock()
			g.posChan <- g.latestPos
			g.latestPosMu.RUnlock()
		}
	}
}

type Request struct {
	Start bool
	Stop  bool
}

func (g *GPSr) recording(ctx context.Context) error {
	statusMu.Lock()
	status = true
	statusMu.Unlock()
	logger.Printf("Starting perpetual recording")
	defer logger.Printf("recording stopped")

	for {
		err := g.record(ctx)
		select {
		case <-ctx.Done():
			return err
		default:
		}
		if err != nil {
			return fmt.Errorf("recording error: %v", err)
		}
	}
}

func (g *GPSr) record(ctx context.Context) (err error) {
	var port io.ReadCloser
	if g.fakeDevice {
		port, err = os.Open(g.config.CaptureDevice)
	} else {
		port, err = serial.Open(g.config.CaptureDevice,
			&serial.Mode{
				BaudRate: g.config.BaudRate,
			})
	}
	if err != nil {
		return fmt.Errorf("while opening capture device: %w", err)
	}

	defer func() {
		if closeErr := port.Close(); closeErr != nil {
			err = utils.MaybeWrap(closeErr, err, "while closing capturing device")
			return
		}
	}()

	return g.recordWithDuration(ctx, port, g.config.SplitDuration)
}

func (g *GPSr) recordWithDuration(ctx context.Context, port io.ReadCloser, timeout time.Duration) (err error) {
	logger.Printf("starting new recording for %v", timeout)
	tm := time.NewTimer(timeout)
	g.rmc = []datatype.SRMCplus{}

	count := 0
	defer func() {
		if count == 0 {
			logger.Printf("not creating any file, because there was no data")
			return
		}

		err = g.finalize(err)
	}()

	// detect that we're actually getting a read, in case we picked up an
	// irrelevant serial device, instead of the GPS one.
	var canRead chan bool = make(chan bool)
	go func() {
		defer close(canRead)
		select {
		case <-ctx.Done():
			return
		case canRead <- true:
		case <-time.After(30 * time.Second):
			logger.Printf("no data from capture device %s. Is it actually a GPS device?", g.config.CaptureDevice)
		}
	}()

	scanner := bufio.NewScanner(port)
	for scanner.Scan() {
		<-canRead
		select {
		case <-ctx.Done():
			if g.config.Verbose {
				logger.Printf("recording interrupted")
			}
			return nil
		case <-tm.C:
			if g.config.Verbose {
				logger.Printf("recording part reached maximum size")
			}
			return nil
		default:
		}

		isRMC, err := g.parseGPS(scanner.Text())
		if err != nil {
			logger.Printf("while handling GPS data: %v", err)
		}

		if isRMC {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		// wrapping because we want to catch serial.PortClosed at the top level
		return fmt.Errorf("while scanning GPS data: %w", err)
	}
	return nil
}

func (g *GPSr) finalize(oldErr error) error {
	// write in a temp dir, because we want to finish with a rename in the fsnotify
	// watched dir.
	tmpDir := filepath.Join(g.config.DataDir, datatype.TmpDirName)
	if dirErr := os.MkdirAll(tmpDir, os.ModePerm); dirErr != nil {
		return utils.MaybeWrap(dirErr, oldErr, fmt.Sprintf("while creating temp destination dir %v", tmpDir))
	}

	of, creatErr := datatype.CreateFile(tmpDir, datatype.GPS)
	if creatErr != nil {
		return utils.MaybeWrap(creatErr, oldErr, "while creating recording file")
	}

	enc := json.NewEncoder(of)
	if encErr := enc.Encode(g.rmc); encErr != nil {
		return utils.MaybeWrap(encErr, oldErr, "while encoding RMC objects to JSON")
	}

	name := of.Name()
	if closeErr := of.Close(); closeErr != nil {
		return utils.MaybeWrap(closeErr, oldErr, fmt.Sprintf("while closing file %v", name))
	}

	newName := filepath.Join(g.config.DataDir, filepath.Base(name))
	if rnErr := os.Rename(name, newName); rnErr != nil {
		return utils.MaybeWrap(rnErr, oldErr, fmt.Sprintf("while renaming file %v", name))
	}
	logger.Printf("recording saved: %v", newName)

	return oldErr
}

func (g *GPSr) parseGPS(l string) (bool, error) {
	var msg nmea.Sentence
	var err error
	if g.fakeDevice {
		msg, err = (&nmea.SentenceParser{
			// ignore CRC for tests
			CheckCRC: func(nmea.BaseSentence, string) error {
				return nil
			},
		}).Parse(l)
	} else {
		msg, err = nmea.Parse(l)
	}
	if err != nil {
		return false, fmt.Errorf("NMEA parsing error for %v: %w", l, err)
	}
	dt := msg.DataType()
	if dt != nmea.TypeRMC && dt != nmea.TypeGGA {
		return false, nil
	}

	if dt == nmea.TypeGGA {
		gga, ok := msg.(nmea.GGA)
		if !ok {
			return false, errors.New("message type does not match DataType (GGA)")
		}
		g.dop = gga.HDOP
		return false, nil
	}

	mrmc, ok := msg.(nmea.RMC)
	if !ok {
		return false, errors.New("message type does not match DataType (RMC)")
	}
	rmc := trimRMC(mrmc)
	rmcp := datatype.SRMCplus{
		HDOP: g.dop,
		SRMC: rmc,
	}

	if g.config.KeepInvalid {
		g.rmc = append(g.rmc, rmcp)
		return true, nil
	}

	if rmc.Validity == "V" {
		if g.config.Verbose {
			// logger.Printf("ignoring invalid RMC data")
		}
		return false, nil
	}

	if !rmc.Date.Valid || !rmc.Time.Valid {
		if g.config.Verbose {
			logger.Printf("ignoring RMC data with invalid date or time")
		}
		return false, nil
	}

	g.rmc = append(g.rmc, rmcp)

	g.latestPosMu.Lock()
	defer g.latestPosMu.Unlock()
	g.latestPos.Latitude = rmcp.SRMC.Latitude
	g.latestPos.Longitude = rmcp.SRMC.Longitude
	return true, nil
}

// TODO: duplicated in pkg/lpm. refactor.
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
