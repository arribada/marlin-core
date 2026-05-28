// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package fake

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/arribada/marlin-common/pkg/sat"
	"github.com/arribada/marlin-core/collector/pkg/lpm/pb"
	"google.golang.org/protobuf/proto"
)

const lpmDevice = "/tmp/lpm"

var (
	portr io.ReadWriter
	portw io.ReadWriter
)

func Run(ctx context.Context) error {
	lpmDeviceR := lpmDevice + "r"
	lpmDeviceW := lpmDevice + "w"
	if err := os.RemoveAll(lpmDeviceR); err != nil {
		return err
	}
	if err := syscall.Mkfifo(lpmDeviceR, 0666); err != nil {
		return err
	}
	fr, err := os.OpenFile(lpmDeviceR, os.O_RDWR, os.ModeNamedPipe)
	if err != nil {
		return err
	}
	defer fr.Close()
	portr = fr

	if err := os.RemoveAll(lpmDeviceW); err != nil {
		return err
	}
	if err := syscall.Mkfifo(lpmDeviceW, 0666); err != nil {
		return err
	}
	fw, err := os.OpenFile(lpmDeviceW, os.O_RDWR, os.ModeNamedPipe)
	if err != nil {
		return err
	}
	defer fw.Close()
	portw = fw

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	return getCoreMsgs(ctx)
}

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
	println(lat, long, tm.Format(time.TimeOnly))

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

	return sendReply(msgId, nil)
}

func onModeMsg(ctx context.Context, msgId uint32, msg *pb.CoreMessage_Mode) error {
	if msgId < 3 {
		return fmt.Errorf("msg %d should be an init, not a mode", msgId)
	}

	mode := msg.Mode
	if mode == nil {
		return errors.New("nil mode")
	}

	println("HARBOUR MODE: ", mode.Cmd)
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
