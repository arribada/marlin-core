// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package sat

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestEncodeLatitude(t *testing.T) {
	f := float64(-90)
	counter := 0
	for {
		buf := encodeLatitude(f)
		g := decodeRaw(buf)
		if f-g >= math.Abs(0.0001) {
			t.Fatalf("wanted %f, got %f", f, g)
		}
		f += 0.0001
		if f >= 90 {
			break
		}
		counter++
	}
	println("checked", counter, "values")
}

func TestEncodeLongitude(t *testing.T) {
	f := float64(-180)
	counter := 0
	for {
		buf := encodeLongitude(f)
		g := decodeRaw(buf)
		if f-g >= math.Abs(0.0001) {
			t.Fatalf("wanted %f, got %f", f, g)
		}
		f += 0.0001
		if f >= 180 {
			break
		}
		counter++
	}
	println("checked", counter, "values")
}

func TestGenRawData(t *testing.T) {
	tm := time.Now()
	lat := 45.6789
	long := 123.4567
	buf, _ := GenRawDataHex(lat, long, tm, LDA2Size)

	lat2, long2, tm2, err := DecodeRawDataHex(buf)
	if err != nil {
		t.Fatal(err)
	}

	if lat-lat2 >= math.Abs(0.0001) {
		t.Fatalf("wrong latitude, wanted %f, got %f", lat, lat2)
	}

	if long-long2 >= math.Abs(0.0001) {
		t.Fatalf("wrong longitude, wanted %f, got %f", long, long2)
	}

	wantHour, wantMinute, wantSecond := tm.Hour(), tm.Minute(), tm.Second()
	hour, minute, second := tm2.Hour(), tm2.Minute(), tm2.Second()
	if tm.Hour() != hour {
		t.Fatalf("wrong hour, wanted %d, got %d", wantHour, hour)
	}
	if tm.Minute() != minute {
		t.Fatalf("wrong minute, wanted %d, got %d", wantMinute, minute)
	}
	if tm.Second() != second {
		t.Fatalf("wrong second, wanted %d, got %d", wantSecond, second)
	}
}

func TestSafeDecode(t *testing.T) {
	buf := []byte("blabla")
	_, _, _, err := DecodeRawData(buf)
	if err == nil {
		t.Fatal("should have been an error")
	}
	if !strings.HasPrefix(err.Error(), "invalid raw data length") {
		t.Fatalf("unexpected error: %v", err)
	}
}
