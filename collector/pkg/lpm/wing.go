// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package lpm

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/arribada/marlin-core/collector/pkg/lpm/pb"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
	"google.golang.org/protobuf/proto"
)

type Config struct {
	Enabled bool
	Verbose bool

	Device   string
	DeviceID string
	ID       string
	Addr     string
	SecKey   string
	Rconf    string
}

const (
	LDA2Size = 24 // LDA2 is 196 bits, but 192 bits is ok too.
)

var (
	// TODO: handle concurrent access?
	port serial.Port
	// TODO: something cleaner. cannot use port itself as a sentinel because it is an interface.
	initialized bool
	msgID       uint32 = 1 // 0 is reserved for unmarshaling errors

	rcvdMu sync.RWMutex
	rcvd   map[uint32]pb.LpmReply_Response
)

type WingParams struct {
	ID     string
	SecKey string
	Addr   string
	Rconf  string
}

func Run(cfg Config, ctx context.Context) error {
	if !cfg.Enabled {
		return nil
	}

	if cfg.Device == "" {
		dev, err := findSerialUSBDevice(cfg.DeviceID, true)
		if err != nil {
			return fmt.Errorf("while trying to automatically find LPM device: %v", err)
		}
		cfg.Device = dev
	}

	log.Printf("LPM device: %v", cfg.Device)
	rcvd = make(map[uint32]pb.LpmReply_Response)
	var err error
	port, err = serial.Open(cfg.Device,
		&serial.Mode{
			BaudRate: 115200,
		})
	if err != nil {
		return fmt.Errorf("while opening LPM as serial device: %v", err)
	}

	defer func() {
		if err := port.Close(); err != nil {
			log.Printf("while closing serial connection to LPM: %v", err)
		}
	}()

	if !initialized {
		initRetry(cfg, ctx)
	}

	go getReplies(ctx)

	<-ctx.Done()
	return nil
}

// coping mechanism for the fact that one time out of two, first message ends up in failure.
// TODO: investigate why. And when we figure it out, remove the
// whole retry mechanism, as we have one at the caller level now.
func initRetry(cfg Config, ctx context.Context) {
	pause := time.Second
	maxPause := 5 * time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		go getReply()
		time.Sleep(time.Second)
		err := initSMD(WingParams{
			ID:     cfg.ID,
			SecKey: cfg.SecKey,
			Addr:   cfg.Addr,
			Rconf:  cfg.Rconf,
		})
		if err == nil {
			initialized = true
			return
		}
		log.Printf("sat comms: while setting up SMD: %v", err)
		log.Printf("sat comms: retrying in %v", pause)
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
		pause *= 2
		if pause > maxPause {
			pause = maxPause
		}
	}
}

func wingParamsMessage(params WingParams) *pb.CoreMessage {
	wing := pb.SatInit{
		Id:     params.ID,
		Addr:   params.Addr,
		SecKey: params.SecKey,
		Rconf:  params.Rconf,
	}

	return &pb.CoreMessage{
		MsgId: msgID,
		Message: &pb.CoreMessage_Init{
			Init: &wing,
		},
	}
}

func initSMD(params WingParams) error {
	wingParamsMsg := wingParamsMessage(params)
	id := msgID
	msgID++

	out, err := proto.Marshal(wingParamsMsg)
	if err != nil {
		return fmt.Errorf("while encoding wing params message %d: %v", id, err)
	}

	return send(out, id)
}

func sendTX(msg []byte) error {
	if !initialized {
		return fmt.Errorf("Wing not initialized")
	}
	prefix := "AT+TX="
	if len(msg) > LDA2Size {
		return fmt.Errorf("message too long: want %d, got %d", LDA2Size, len(msg))
	}

	asHex := make([]byte, LDA2Size*2)
	hex.Encode(asHex, msg)

	data := prefix + string(asHex)
	if err := sendSatMessage(data); err != nil {
		return err
	}

	return nil
}

func sendSatMessage(msg string) error {
	satMsg := satMessage(msg)
	id := msgID
	msgID++

	out, err := proto.Marshal(satMsg)
	if err != nil {
		return fmt.Errorf("while encoding sat message %d: %v", id, err)
	}

	return send(out, id)
}

func send(data []byte, id uint32) error {
	log.Printf("SENDING MESSAGE: %v", string(data))
	if _, err := port.Write(data); err != nil {
		return fmt.Errorf("while sending message: %v", err)
	}
	log.Println("MESSAGE SENT")

	tm := time.NewTimer(60 * time.Second)
	for {
		select {
		case <-tm.C:
			return fmt.Errorf("timed out waiting for answer for msg %d", id)
		default:
		}
		rcvdMu.RLock()
		rply, ok := rcvd[id]
		rcvdMu.RUnlock()
		if !ok {
			// response not received yet
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if rply == pb.LpmReply_ERROR {
			return fmt.Errorf("ERROR response to msg %d", id)
		}
		if rply == pb.LpmReply_OK {
			log.Printf("OK response to msg %d", id)
			break
		}
		// TODO: remove
		return fmt.Errorf("FILLER response to msg %d", id)
	}
	return nil
}

func satMessage(msg string) *pb.CoreMessage {
	sat := pb.SatMessage{
		Message: msg,
	}

	return &pb.CoreMessage{
		MsgId: msgID,
		Message: &pb.CoreMessage_Sat{
			Sat: &sat,
		},
	}
}

func getReplies(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := getReply(); err != nil {
			log.Printf("%v", err)
			continue
		}
	}
}

func getReply() error {
	// TODO: do not allocate everytime?
	p := make([]byte, 1024)
	n, err := port.Read(p)
	if err != nil {
		return fmt.Errorf("sat comms: while reading reply: %v", err)
	}
	if n == 0 {
		return fmt.Errorf("sat comms: empty reply")
	}
	data := p[:n]
	for _, v := range data {
		fmt.Printf("%x ", v)
	}

	var reply pb.LpmReply
	if err := proto.Unmarshal(data, &reply); err != nil {
		return fmt.Errorf("sat comms: while decoding message: %v", err)
	}
	fmt.Println("REPLY: ", reply)

	rcvdMu.Lock()
	rcvd[reply.MsgId] = reply.Response
	rcvdMu.Unlock()
	return nil
}

// TODO: duplicated in pkg/node/gpsr. refactor.
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
