// Copyright (c) The Arribada initiative.
// Licensed under the MIT License.

package gc

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arribada/marlin-core/common/pkg/datatype"
)

var (
	audioPrefix, _ = datatype.FilePrefix(datatype.Audio)
	sttPrefix, _   = datatype.FilePrefix(datatype.Speech)
	audioSuffix, _ = datatype.FileExt(datatype.Audio)
	sttSuffix, _   = datatype.FileExt(datatype.Speech)
	sttDir, _      = datatype.DirType(datatype.Speech)
)

type testHook struct {
	found   map[string]struct{}
	removed map[string]struct{}
}

type GC struct {
	RootDir     string
	Verbose     bool
	dry         bool
	GracePeriod int // in seconds
	hook        *testHook
}

func (g GC) Run() error {
	fn := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// TODO: maybe remove empty (audio) dirs?
		if d.IsDir() {
			return nil
		}

		name := d.Name()
		if g.hook != nil {
			g.hook.found[p] = struct{}{}
		}

		if !strings.HasPrefix(name, audioPrefix) {
			return nil
		}

		if !strings.HasSuffix(name, audioSuffix) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		modTime := info.ModTime()
		graceLimit := modTime.Add(time.Duration(g.GracePeriod) * time.Second)
		// if file is not more than a day old, leave it alone
		if time.Now().Before(graceLimit) {
			return nil
		}

		// if there's no matching stt file, we consider it garbage
		currentDir := filepath.Dir(p)
		sttdir := filepath.Join(currentDir, "..", sttDir)
		timestamp := strings.TrimPrefix(name, audioPrefix+"-")
		timestamp = strings.TrimSuffix(timestamp, "."+audioSuffix)
		sttName := sttPrefix + "-" + timestamp + "." + sttSuffix
		sttPath := filepath.Join(sttdir, sttName)
		_, err = os.Stat(sttPath)
		if err == nil {
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("unexpected stat error: %v", err)
		}
		if g.Verbose {
			log.Printf("%v has no matching stt file, so removing it", name)
		}

		if !g.dry {
			if err := os.Remove(p); err != nil {
				return fmt.Errorf("unexpected remove error: %v", err)
			}
		}
		if g.hook != nil {
			g.hook.removed[p] = struct{}{}
		}
		return nil
	}

	return filepath.WalkDir(g.RootDir, fn)
}
