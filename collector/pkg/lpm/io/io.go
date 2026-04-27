// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package io

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/arribada/marlin-common/pkg/sat"
	"github.com/arribada/marlin-core/collector/pkg/lpm/pb"

	"go.bug.st/serial"
	"google.golang.org/protobuf/proto"
)

const (
	LDA2Size = 24 // LDA2 is 196 bits, but 192 bits is ok too.
)

var (
	logger = log.New(os.Stdout, "LPM: ", log.Default().Flags()|log.Lmsgprefix)

	mu          sync.RWMutex
	initialized bool
	port        serial.Port

	idMu  sync.RWMutex
	msgID uint32 = 1 // 0 is reserved for unmarshaling errors

	rcvdMu sync.RWMutex
	rcvd   map[uint32]pb.LpmReply_Response = make(map[uint32]pb.LpmReply_Response)
)

func OpenPort(device string) (serial.Port, error) {
	p, err := serial.Open(device,
		&serial.Mode{
			BaudRate: 115200,
		})
	if err != nil {
		return nil, err
	}
	port = p
	return p, nil
}

// coping mechanism for the fact that one time out of two, first message ends up in failure.
// TODO: investigate why. And when we figure it out, remove the
// whole retry mechanism, as we have one at the caller level now.
func InitRetry(sysInit pb.SysInit, ctx context.Context) {
	mu.RLock()
	if initialized {
		mu.RUnlock()
		return
	}
	mu.RUnlock()

	pause := time.Second
	maxPause := 5 * time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		go GetReply()
		time.Sleep(time.Second)
		err := sendSysInit(sysInit)
		if err == nil {
			return
		}
		logger.Printf("while setting up SMD: %v", err)
		logger.Printf("retrying in %v", pause)
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

	logger.Printf("SENDING MESSAGE: %v", string(data))
	if _, err := port.Write(data); err != nil {
		return fmt.Errorf("while sending message: %v", err)
	}
	logger.Println("MESSAGE SENT")

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
			logger.Printf("OK response to msg %d", id)
			break
		}
		// TODO: remove
		return fmt.Errorf("FILLER response to msg %d", id)
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
			logger.Printf("%v", err)
			continue
		}
	}
}

// TODO: make it cancellable (and with timeout) ?
func GetReply() error {
	// TODO: do not allocate everytime?
	p := make([]byte, 1024)
	n, err := port.Read(p)
	if err != nil {
		return fmt.Errorf("while reading reply: %v", err)
	}
	if n == 0 {
		return fmt.Errorf("empty reply")
	}
	data := p[:n]
	for _, v := range data {
		fmt.Printf("%x ", v)
	}

	var reply pb.LpmReply
	if err := proto.Unmarshal(data, &reply); err != nil {
		return fmt.Errorf("while decoding message: %v", err)
	}
	fmt.Println("REPLY: ", reply)

	rcvdMu.Lock()
	rcvd[reply.MsgId] = reply.Response
	rcvdMu.Unlock()
	return nil
}

func SendCoreStatus(status pb.CoreStatus) error {
	idMu.Lock()
	id := msgID
	msg := &pb.CoreMessage{
		MsgId: id,
		Message: &pb.CoreMessage_Status{
			Status: &status,
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

func sendSysInit(sysInit pb.SysInit) error {
	idMu.Lock()
	id := msgID
	msg := &pb.CoreMessage{
		MsgId: id,
		Message: &pb.CoreMessage_Init{
			Init: &sysInit,
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

	data, err := AlertToBytes(alert)
	if err != nil {
		return fmt.Errorf("while packing alert as bytes stream: %v", err)
	}

	if err := sendTX(data); err != nil {
		return fmt.Errorf("while sending alert: %v", err)
	}
	return nil
}
