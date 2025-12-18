// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/arribada/marlin-core/lpm/cmd/lpm/pb"

	"go.bug.st/serial"
	"google.golang.org/protobuf/proto"
)

var (
	flagDevice string
)

func init() {
	flag.StringVar(&flagDevice, "device", "/dev/cu.usbmodem1101", "device")
}

// TODO: Makefile to drive protoc

const (
	LDA2Size = 48 // We need 196 bits, but 192 bits should be ok too.
)

var (
	satWindow = 30 * time.Minute
	satRetry  = 1 * time.Minute
)

var (
	port  serial.Port
	msgID uint32 = 1 // 0 is reserved for unmarshaling errors

	rcvdMu sync.RWMutex
	rcvd   map[uint32]pb.LpmReply_Response
)

func satMessage(msg string) *pb.CoreMessage {
	sat := pb.SatMessage{
		Message: msg,
	}

	return &pb.CoreMessage{
		MsgId: uint32(msgID),
		Message: &pb.CoreMessage_Sat{
			Sat: &sat,
		},
	}
}

func sendAT(atCmd string) error {
	msg := satMessage(atCmd)
	id := msgID
	msgID++

	out, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("while encoding message: %v", err)
	}
	log.Printf("SENDING MESSAGE: %v", string(out))
	if _, err := port.Write(out); err != nil {
		return fmt.Errorf("while sending message: %v", err)
	}
	log.Println("MESSAGE SENT")

	tm := time.NewTimer(60 * time.Second)
	for {
		select {
		case <-tm.C:
			return fmt.Errorf("timed out waiting for answer for msg %d: %v", id, atCmd)
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
			return fmt.Errorf("ERROR response to msg %d: %v", id, atCmd)
		}
		if rply == pb.LpmReply_OK {
			log.Printf("OK response to msg %d: %v", id, atCmd)
			break
		}
		return fmt.Errorf("FILLER response to msg %d: %v", id, atCmd)
	}
	return nil
}

var atInitCmds = []string{
	"AT+FW=?",
	"AT+ID=291886",
	"AT+ID=?",
	"AT+ADDR=005A9F10",
	"AT+ADDR=?",
	"AT+SECKEY=F179706C2BD09B5B4D15E69CE212019F",
	"AT+SECKEY=?",
	"AT+RCONF=2c93600d6be3bac0ccfe9047c02c058e", // means LDA2 , so 24 bytes (or lower 2 mupltiples) TX.
	"AT+KMAC=1",
}

func initSat() error {
	for _, v := range atInitCmds {
		if err := sendAT(v); err != nil {
			log.Printf("%v", err)
		}
	}
	return nil
}

func forgeMsg(sample string) []byte {
	msg := make([]byte, 48)
	for i := 0; i < 48; i++ {
		msg[i] = byte(sample[0])
	}
	return msg
}

// TODO: hex encoding!
func sendSatMsg(msg []byte) error {
	prefix := "AT+TX="
	if len(msg) > LDA2Size {
		return fmt.Errorf("message too long: want %d, got %d", LDA2Size, len(msg))
	}

	/*
		// TODO: more efficient
		for k, _ := range padded {
			if k < len(asBytes) {
				padded[k] = asBytes[k]
				continue
			}
			padded[k] = '7'
		}
	*/

	//		dummyMsg := "123456789abcdefghijklmno"
	//	dummyMsg := "FFFFFFFF"
	//	dummyMsg := "FFFFFFFFFFFFFFFF"
	// dummyMsg := "123456789abcdefghijklmno"
	//	dummyMsg := "FFFFFFFFFFFFFFFFFFFFFFFF"
	// dummyMsg := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	data := prefix + string(msg)
	if err := sendAT(data); err != nil {
		return err
	}

	return nil
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
			log.Printf("while reading reply: %v", err)
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
			log.Printf("while decoding message: %v", err)
			continue
		}
		fmt.Println("REPLY: ", reply)

		rcvdMu.Lock()
		rcvd[reply.MsgId] = reply.Response
		rcvdMu.Unlock()
	}
}

func main() {
	flag.Parse()

	rcvd = make(map[uint32]pb.LpmReply_Response)
	var err error
	port, err = serial.Open(flagDevice,
		&serial.Mode{
			BaudRate: 115200,
		})
	if err != nil {
		log.Fatalf("while opening serial device: %v", err)
	}
	defer func() {
		if err := port.Close(); err != nil {
			log.Printf("while closing capturing device: %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go getReplies(ctx)

	if err := initSat(); err != nil {
		log.Fatal(err)
	}

	time.Sleep(5 * time.Second)

	samples := []string{
		"A",
		"B",
		"C",
		"D",
		"E",
		"F",
	}
	sampleIdx := 0

	tm := time.NewTimer(satWindow)
	for {
		select {
		case <-tm.C:
			log.Printf("OVER")
			return
		default:
		}
		msg := forgeMsg(samples[sampleIdx])
		sampleIdx++
		if sampleIdx >= len(samples) {
			sampleIdx = 0
		}
		if err := sendSatMsg(msg); err != nil {
			log.Printf("while sending sat message: %v", err)
		}
		time.Sleep(satRetry)
	}
}

/*

	status := pb.CoreStatus{
		Camera:          pb.CoreStatus_OK,
		Controller:      pb.CoreStatus_OK,
		CloudConnection: pb.CoreStatus_OK,
		StorageUsage:    float32(i),
	}
	msg := &pb.CoreMessage{
		MsgId: uint32(i),
		Message: &pb.CoreMessage_Status{
			Status: &status,
		},
	}
*/
