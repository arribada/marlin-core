// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package lpm

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/arribada/marlin-core/collector/pkg/lpm/pb"

	"go.bug.st/serial"
	"google.golang.org/protobuf/proto"
)

type Config struct {
	Enabled bool
	Verbose bool

	Device string
	ID     string
	Addr   string
	SecKey string
	Rconf  string
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

// TODO: make sat errors non critical to the rest of the collector?
func Run(cfg Config, ctx context.Context) error {
	if !cfg.Enabled {
		return nil
	}

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

	go getReplies(ctx)
	time.Sleep(time.Second)

	if !initialized {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err := initSMD(WingParams{
			ID:     cfg.ID,
			SecKey: cfg.SecKey,
			Addr:   cfg.Addr,
			Rconf:  cfg.Rconf,
		}); err != nil {
			return fmt.Errorf("while setting up SMD: %v", err)
		}
		initialized = true
	}

	<-ctx.Done()
	return nil
}

func Stop(cancel context.CancelFunc) {
	log.Printf("sat comms stopped")
	cancel()
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
		p := make([]byte, 1024)
		n, err := port.Read(p)
		if err != nil {
			log.Printf("sat comms: while reading reply: %v", err)
			continue
		}
		println("reply size", n)
		if n == 0 {
			continue
		}
		data := p[:n]
		for _, v := range data {
			fmt.Printf("%x ", v)
		}

		var reply pb.LpmReply
		if err := proto.Unmarshal(data, &reply); err != nil {
			log.Printf("sat comms: while decoding message: %v", err)
			continue
		}
		fmt.Println("REPLY: ", reply)

		rcvdMu.Lock()
		rcvd[reply.MsgId] = reply.Response
		rcvdMu.Unlock()
	}
}
