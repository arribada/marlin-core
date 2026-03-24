// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package rtsp

import (
	"runtime"
	"testing"
)

func TestFindCaptureDevice(t *testing.T) {
	// TODO: do better later
	if runtime.GOOS != "darwin" {
		return
	}

	for _, v := range []string{
		// old hilook style URL
		"rtsp://foo:bar@192.168.0.N:554/Streaming/Channels/101/?transportmode=unicast",
		// vivotek style URL
		"rtsp://foo:bar@192.168.0.N:554/live1s1.sdp",
	} {
		testFindCaptureDevice(v, t)
	}
}

func testFindCaptureDevice(rtsp string, t *testing.T) {
	nf, _, err := needsFinding(rtsp)
	if err != nil {
		t.Fatal(err)
	}
	if !nf {
		t.Fatal("should be needed")
	}

	dev, err := findCaptureDevice(rtsp)
	if err != nil {
		t.Fatal(err)
	}
	println(dev)

	nf, _, err = needsFinding(dev)
	if err != nil {
		t.Fatal(err)
	}
	if nf {
		t.Fatal("we should not need to search for it again here")
	}
}
