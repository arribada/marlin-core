// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package io

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/arribada/marlin-common/pkg/sat"
	"github.com/arribada/marlin-core/collector/pkg/lpm/battery"
	"github.com/arribada/marlin-core/collector/pkg/lpm/pb"

	"go.bug.st/serial"
	"google.golang.org/protobuf/proto"
)

var logger = log.New(os.Stderr, "LPM: ", log.Default().Flags()|log.Lmsgprefix)

const (
	LDA2Size = 24 // LDA2 is 196 bits, but 192 bits is ok too.
)

var (
	Verbose bool

	mu          sync.RWMutex
	initialized bool

	// TODO: these two could/should just be one serial.Port, but we're
	// using a named pipe file in tests, which cannot be multiplexed. We
	// probably can do better and avoid be affected here, but later.
	portr io.ReadWriteCloser
	portw io.ReadWriteCloser

	idMu  sync.RWMutex
	msgID uint32 = 1 // 0 is reserved for unmarshaling errors

	rcvdMu sync.RWMutex
	rcvd   map[uint32]*pb.LpmReply = make(map[uint32]*pb.LpmReply)

	AlertErrC = make(chan error)
)

type namedPipe struct {
	r *os.File
	w *os.File
}

func (np namedPipe) Close() error {
	err1 := np.r.Close()
	err2 := np.w.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

func OpenPort(device string) (io.Closer, error) {
	// When using a named pipe, instead of a real device, in tests.
	if !strings.HasPrefix(device, "/dev") {
		pr, err := os.OpenFile(device+"r", os.O_RDWR, os.ModeNamedPipe)
		if err != nil {
			return nil, err
		}
		pw, err := os.OpenFile(device+"w", os.O_RDWR, os.ModeNamedPipe)
		if err != nil {
			return nil, err
		}
		portr = pw
		portw = pr
		return namedPipe{
			r: pr,
			w: pw,
		}, nil
	}

	p, err := serial.Open(device,
		&serial.Mode{
			BaudRate: 115200,
		})
	if err != nil {
		return nil, err
	}
	portr = p
	portw = p
	return p, nil
}

func InitRetry(sysInit *pb.SysInit, ctx context.Context) error {
	mu.RLock()
	if initialized {
		mu.RUnlock()
		return nil
	}
	mu.RUnlock()

	var err error
	// coping mechanism for the fact that one time out of two, first message ends up in failure.
	// TODO: investigate why.
	for i := 0; i < 2; i++ {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		go GetReply()
		time.Sleep(time.Second)
		err = sendSysInit(sysInit)
		if err == nil {
			break
		}

		time.Sleep(time.Second)
	}

	return err
}

func sendTX(msg []byte) error {
	prefix := "AT+TX="
	if len(msg) > LDA2Size {
		return fmt.Errorf("message too long: want %d, got %d", LDA2Size, len(msg))
	}
	asHex := make([]byte, LDA2Size*2)
	hex.Encode(asHex, msg)
	data := prefix + string(asHex)

	idMu.Lock()
	id := msgID
	satMsg := &pb.CoreMessage{
		MsgId: id,
		Message: &pb.CoreMessage_Sat{
			Sat: &pb.SatMessage{
				Message: data,
			},
		},
	}
	msgID++
	idMu.Unlock()

	out, err := proto.Marshal(satMsg)
	if err != nil {
		return fmt.Errorf("while encoding sat message %d: %v", id, err)
	}

	return send(out, id, true)
}

// TODO: make it context cancellable?
func send(data []byte, id uint32, initCheck bool) error {
	// TODO: that's a big long lock, to prevent e.g. sendStatus and
	// sendAlert to race each other. Maybe we could do better/finer, but
	// might be harder to get it right. Because we have to think about the
	// concurrent reads in GetReply as well.
	mu.Lock()
	defer mu.Unlock()
	if initCheck && !initialized {
		return fmt.Errorf("Wing not initialized")
	}

	if Verbose {
		logger.Printf("sending message: %x", data)
	}
	if _, err := portw.Write(data); err != nil {
		return fmt.Errorf("while sending message: %v", err)
	}
	if Verbose {
		logger.Printf("message sent")
	}

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
		if rply.Response == pb.LpmReply_ERROR {
			return fmt.Errorf("ERROR response to msg %d", id)
		}

		// rply.Response == pb.LpmReply_OK
		if Verbose {
			logger.Printf("OK response to msg %d", id)
		}

		// TODO: remove (unneeded) Set field from proto. Since BatteryStatus is a
		// pointer field, it will be nil if it isn't touched on the LPM side.
		if rply.BatteryStatus == nil || !rply.BatteryStatus.Set {
			// do not update the battery voltage if LPM hasn't set it
			break
		}
		battery.Mu.Lock()
		battery.Voltage = float64(rply.BatteryStatus.Voltage)
		battery.Current = float64(rply.BatteryStatus.Current)
		battery.Mu.Unlock()

		break

	}
	initialized = true
	return nil
}

func GetReplies(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := GetReply(); err != nil {
			logger.Printf("while getting reply: %v", err)
			if errors.Is(err, portClosed) {
				return
			}
			continue
		}
	}
}

var portClosed = errors.New("port closed")

// TODO: make it cancellable (and with timeout) ?
func GetReply() error {
	// TODO: do not allocate everytime?
	p := make([]byte, 1024)
	n, err := portr.Read(p)
	if err != nil {
		pt, ok := err.(*serial.PortError)
		if ok && pt.Code() == serial.PortClosed {
			return portClosed
		}
		return fmt.Errorf("while reading reply: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("empty reply")
	}
	data := p[:n]

	var reply pb.LpmReply
	if err := proto.Unmarshal(data, &reply); err != nil {
		return fmt.Errorf("while decoding message: %v", err)
	}
	if Verbose {
		logger.Printf("reply: %#v", &reply)
	}

	rcvdMu.Lock()
	rcvd[reply.MsgId] = &reply
	rcvdMu.Unlock()
	return nil
}

func SendHarbourMode(hm *pb.HarbourMode) error {
	idMu.Lock()
	id := msgID
	msg := &pb.CoreMessage{
		MsgId: id,
		Message: &pb.CoreMessage_Mode{
			Mode: hm,
		},
	}
	msgID++
	idMu.Unlock()

	out, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("while encoding Harbour Mode message %d: %v", id, err)
	}

	return send(out, id, true)
}

func SendCoreStatus(status *pb.CoreStatus) error {
	idMu.Lock()
	id := msgID
	msg := &pb.CoreMessage{
		MsgId: id,
		Message: &pb.CoreMessage_Status{
			Status: status,
		},
	}
	msgID++
	idMu.Unlock()

	out, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("while encoding CoreStatus message %d: %v", id, err)
	}

	return send(out, id, true)
}

func sendSysInit(sysInit *pb.SysInit) error {
	idMu.Lock()
	id := msgID
	msg := &pb.CoreMessage{
		MsgId: id,
		Message: &pb.CoreMessage_Init{
			Init: sysInit,
		},
	}
	msgID++
	idMu.Unlock()

	out, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("while encoding SysInit message %d: %v", id, err)
	}

	return send(out, id, false)
}

func AlertToBytes(alert sat.Alert) ([]byte, error) {
	return sat.GenRawData(alert.Latitude, alert.Longitude, alert.Time, sat.LDA2Size)
}

func SendAlert(alert sat.Alert) error {
	logger.Printf("Sending Alert for position (%f, %f) at %v", alert.Latitude, alert.Longitude, alert.Time)

	// Even though we're returning the error, since we're a public func,
	// we don't know how it's caller (currently the data watcher) is going
	// to handle it. And we want to bubble it up, so we can cleanly log
	// it and capture it in Goog Cloud Logging. So we bubble it up with
	// the chan as well.
	data, err := AlertToBytes(alert)
	if err != nil {
		err = fmt.Errorf("while packing alert as bytes stream: %v", err)
		select {
		case AlertErrC <- err:
		case <-time.After(time.Second):
		}
		return err
	}

	if err := sendTX(data); err != nil {
		err = fmt.Errorf("while sending alert: %v", err)
		select {
		case AlertErrC <- err:
		case <-time.After(time.Second):
		}
		return err
	}
	return nil
}
