// Copyright (c) The Arribada initiative.
// Licensed under the MIT License.

package gc

import (
	"os"
	"testing"
)

func TestGC(t *testing.T) {
	g := GC{
		RootDir:     "./testdata",
		Verbose:     true,
		GracePeriod: 60,
		dry:         true,
		hook: &testHook{
			found:   make(map[string]struct{}),
			removed: make(map[string]struct{}),
		},
	}

	// touch this file rn, to make it recent, so it gets graced and ignored by the GC
	if err := os.WriteFile("testdata/2024-07-27/audio/audio-2024-07-27T08_16_12Z.wav", []byte("touched"), 0700); err != nil {
		t.Fatal(err)
	}

	if err := g.Run(); err != nil {
		t.Fatal(err)
	}

	wantFound := map[string]struct{}{
		"testdata/2024-07-27/stt/stt-2024-07-27T08_16_19Z.json":         {},
		"testdata/2024-07-27/stt/stt-2024-07-27T08_23_29Z.json":         {},
		"testdata/2024-07-27/video/video-2024-07-27T07_55_18Z.mp4":      {},
		"testdata/2024-07-27/audio/audio-2024-07-27T08_16_19Z.wav":      {},
		"testdata/2024-07-27/audio/audio-2024-07-27T08_23_09Z.wav":      {},
		"testdata/2024-07-27/audio/audio-2024-07-27T08_23_29Z.wav":      {},
		"testdata/2024-07-27/fishing/fishing-2024-07-27T09_44_37Z.json": {},
		"testdata/2024-07-27/audio/audio-2024-07-27T08_16_12Z.wav":      {},
		"testdata/2024-07-27/gps/gps-2024-07-27T09_44_36Z.json":         {},
	}

	if len(wantFound) != len(g.hook.found) {
		t.Fatalf("Want %d VS Got %d", len(wantFound), len(g.hook.found))
	}

	for k := range wantFound {
		if _, ok := g.hook.found[k]; !ok {
			t.Fatalf("%s not found", k)
		}
	}

	// 4 audio files. one too recent, two with corresponding stts. So only one to remove.
	wantRemoved := map[string]struct{}{
		"testdata/2024-07-27/audio/audio-2024-07-27T08_23_09Z.wav": {},
	}

	if len(wantRemoved) != len(g.hook.removed) {
		t.Fatalf("Want %d VS Got %d", len(wantRemoved), len(g.hook.removed))
	}

	for k := range wantRemoved {
		if _, ok := g.hook.removed[k]; !ok {
			t.Fatalf("%s not found in removed", k)
		}
	}
}
