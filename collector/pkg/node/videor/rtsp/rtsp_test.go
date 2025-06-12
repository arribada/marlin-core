// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package rtsp

import (
	"testing"
)

func TestFindCaptureDevice(t *testing.T) {
	dev, err := findCaptureDevice("rtsp://foo:bar@192.168.0.N:554/Streaming/Channels/101/?transportmode=unicast")
	if err != nil {
		t.Fatal(err)
	}
	println(dev)

	nf, _, err := needsFinding(dev)
	if err != nil {
		t.Fatal(err)
	}

	if nf {
		t.Fatal("we should not need to search for it again here")
	}
}
