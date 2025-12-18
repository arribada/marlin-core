// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package sat

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"time"
)

type Alert struct {
	Latitude  float64
	Longitude float64
	Time      time.Time
}

const (
	latWidth      = 4
	longWidth     = 5
	rawDataLength = 24
)

func AlertToBytes(alert Alert) ([]byte, error) {
	return GenRawData(alert.Latitude, alert.Longitude, alert.Time, LDA2Size)
}

// TODO: use encoding/gob instead? We could even do it directly on a struct type that contains all three fields.
// 5 bytes for longitude
// 4 bytes for latitude
// 3 bytes for HH:MM:SS
// TODO: rename
func GenRawData(lat, long float64, t time.Time, bufSize int) ([]byte, error) {
	var buf = make([]byte, bufSize)
	// TODO: give the buf as argument to the funcs, to avoid allocations.
	latbuf := encodeLatitude(lat)
	longbuf := encodeLongitude(long)
	for i := 0; i < latWidth; i++ {
		buf[i] = latbuf[i]
	}
	offset := latWidth
	for i := 0; i < longWidth; i++ {
		buf[offset+i] = longbuf[i]
	}
	offset += longWidth
	buf[offset] = byte(t.Hour())
	offset++
	buf[offset] = byte(t.Minute())
	offset++
	buf[offset] = byte(t.Second())

	return buf, nil
}

func genRawDataHex(lat, long float64, t time.Time, bufSize int) ([]byte, error) {
	buf, err := GenRawData(lat, long, t, bufSize)
	if err != nil {
		return nil, err
	}
	asHex := make([]byte, bufSize*2)
	hex.Encode(asHex, buf)
	return asHex, nil
}

// encode latitude at 0.0001 precision on bytes, requires 4 bytes.
// slice returned is 8 bytes long so that it doesn't panic on stdlib binary funcs, but only 4 first bytes are relevant.
func encodeLatitude(lat float64) []byte {
	return encodeAsBytes(lat, latWidth)
}

// encode longitude at 0.0001 precision on bytes, requires 5 bytes. Supports -360 < n < 360 if needed.
// slice returned is 8 bytes long so that it doesn't panic on stdlib binary funcs, but only 5 first bytes are relevant.
func encodeLongitude(long float64) []byte {
	return encodeAsBytes(long, longWidth)
}

func encodeAsBytes(f float64, width int) []byte {
	var buf = make([]byte, 8)
	binary.BigEndian.PutUint64(buf, math.Float64bits(f))

	// Zero out the unneeded part.
	// Should not be necessary, but just to be sure.
	for i := width; i < 8; i++ {
		buf[i] = 0
	}
	return buf
}

func DecodeRawData(buf []byte) (float64, float64, time.Time, error) {
	// TODO: sad we have to allocate to do the decoding. But needed to avoid panicking of stdlib. We should recode it ourselves.
	latBuf := make([]byte, 8)
	copy(latBuf, buf[:latWidth])
	lat := decodeRaw(latBuf)

	offset := latWidth
	longBuf := make([]byte, 8)
	copy(longBuf, buf[offset:offset+longWidth])
	long := decodeRaw(longBuf)

	offset += longWidth
	hour := int(buf[offset])
	offset++
	minute := int(buf[offset])
	offset++
	second := int(buf[offset])

	tm, err := time.Parse(time.TimeOnly, fmt.Sprintf("%02d:%02d:%02d", hour, minute, second))
	if err != nil {
		return 0, 0, time.Time{}, err
	}

	return lat, long, tm, nil
}

func decodeRawDataHex(buf []byte) (float64, float64, time.Time, error) {
	asDecimal := make([]byte, len(buf)/2)
	if _, err := hex.Decode(asDecimal, buf); err != nil {
		return 0, 0, time.Time{}, err
	}
	return DecodeRawData(asDecimal)
}

func decodeRaw(buf []byte) float64 {
	asUint := binary.BigEndian.Uint64(buf)
	asFloat := math.Float64frombits(asUint)
	return asFloat
}

func SendAlert(alert Alert) error {
	log.Printf("Sending Alert for position (%f, %f) at %v", alert.Latitude, alert.Longitude, alert.Time)

	data, err := AlertToBytes(alert)
	if err != nil {
		return fmt.Errorf("while packing alert as bytes stream: %v", err)
	}

	/*
		// TODO: remove
		lat, long, t, err := DecodeRawData(data)
		if err != nil {
			return err
		}
		log.Printf("alert in stream: %f, %f, %v", lat, long, t)
	*/

	if err := sendTX(data); err != nil {
		return fmt.Errorf("while sending alert: %v", err)
	}
	return nil
}
