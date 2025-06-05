// Copyright (c) The Arribada initiative.
// Licensed under the MIT License.

package watcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arribada/marlin-core/common/pkg/datatype"
)

func TestArchiveFile(t *testing.T) {
	dir := t.TempDir()
	dataType := datatype.GPS
	tm := time.Now()

	name, err := datatype.NewFileName(dataType, tm)
	if err != nil {
		t.Fatal(err)
	}
	fp := filepath.Join(dir, name)
	f, err := os.Create(fp)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	ew := &EventsWatcher{
		up:     &uploader{},
		evSeen: make(map[string]bool),
	}
	newName, err := ew.archiveFile(dataType, fp)
	if err != nil {
		t.Fatal(err)
	}

	parts := strings.Split(newName, string(filepath.Separator))
	filename := parts[len(parts)-1]
	dt := parts[len(parts)-2]
	day := parts[len(parts)-3]

	if filename != name {
		t.Fatalf("unexpected filename: wanted %s, got %s", name, filename)
	}

	dtwant, _ := datatype.DirType(dataType)
	if dt != dtwant {
		t.Fatalf("unexpected data type dir: wanted %s, got %s", dtwant, dt)
	}

	today := tm.Format(time.DateOnly)
	if day != today {
		t.Fatalf("unexpected day dir: wanted %s, got %s", today, day)
	}
}
