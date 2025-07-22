// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package gc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/fileerror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/share"
	"github.com/arribada/marlin-core/common/pkg/datatype"
	"golang.org/x/sys/unix"
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

// AudioGC removes all audio files that have already been transcribed
// and older than GracePeriod.
type AudioGC struct {
	RootDir     string
	Verbose     bool
	dry         bool
	GracePeriod int // in seconds
	hook        *testHook
}

func (g AudioGC) Run() error {
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

// GGC is the Generic Garbage Collector, which aims to reclaim space
// on the data storage drive.
// A run is stopped (or not even started) as soon as free storage
// space on the drive is below the defined Threshold.
// A run goes through all data on the drive in lexicographic order,
// which should guarantee that it goes from oldest to newest (since
// we store data as day directories the YYYY-MM-DD format).
// Data removal mainly works along two criteria, depending on the mode.
// In non-aggressive mode, it checks whether each local data file
// is also found on Azure. If yes, it is removed.
// In aggressive mode, everything in a day directory is removed if
// that day directory is older than the defined AgeLimit, without
// checking on Azure.
type GGC struct {
	// RootDir is the root directory for all the data. Usually the mount
	// point for the external drive.
	RootDir    string
	ConnString string // Azure Authentication connection string.
	Vessel     string // Azure share name (usually the name of the vessel).

	// Threshold is the ratio of storage usage above which the GC actually
	// removes things. As soon as usage storage goes back below
	// this threshold, any removal stops.
	Threshold float64

	Aggressive bool          // Remove things without checking on Azure.
	AgeLimit   time.Duration // In aggressive-mode, only remove data older than this limit.

	Verbose bool
	Dry     bool // Whether to actually remove files.
}

func (g GGC) Run() error {
	if g.Aggressive && g.AgeLimit == 0 {
		return errors.New("Aborting because running in Aggressive mode without an AgeLimit is probably not what you want")
	}
	w, u := g.WantSpace()
	if !w {
		if g.Verbose {
			log.Printf("Storage used (%4.2f) below threshold (%4.2f), aborting unneeded GC run", u, g.Threshold)
		}
		return nil
	}
	log.Printf("Storage used (%4.2f) above threshold (%4.2f), GC run needed", u, g.Threshold)

	cl, err := share.NewClientFromConnectionString(g.ConnString, g.Vessel, nil)
	if err != nil {
		return err
	}

	return g.traverse(cl)
}

var NeedSpace func(rootDir string, threshold float64) (bool, float64)

func init() {
	NeedSpace = DefaultNeedSpace
}

// WantSpace returns whether used storage space is above the defined
// threshold. And the ratio of used space over total space.
func (g GGC) WantSpace() (bool, float64) {
	return NeedSpace(g.RootDir, g.Threshold)
}

func DefaultNeedSpace(rootDir string, threshold float64) (bool, float64) {
	var stat unix.Statfs_t
	if err := unix.Statfs(rootDir, &stat); err != nil {
		log.Fatal(err)
	}

	free := stat.Bavail * uint64(stat.Bsize)
	total := stat.Blocks * uint64(stat.Bsize)
	quota := float64(total-free) / float64(total)

	return (quota > threshold), quota
}

func (g GGC) traverse(cl *share.Client) error {
	old := time.Now().Add(-g.AgeLimit)
	rootDir := g.RootDir
	fn := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		w, u := g.WantSpace()
		if !w {
			log.Printf("Storage used (%4.2f) back below threshold (%4.2f), stopping GC run", u, g.Threshold)
			return fs.SkipAll
		}

		if !d.IsDir() {
			return nil
		}

		if p == rootDir {
			return nil
		}

		day := d.Name()
		dayDate, err := time.Parse(time.DateOnly, day)
		if err != nil {
			// not a day directory, skip it
			return fs.SkipDir
		}

		dp := filepath.Join(rootDir, day)
		ent, err := os.ReadDir(dp)
		if err != nil {
			return err
		}
		for _, v := range ent {
			dtName := v.Name()
			if !g.Aggressive {
				if err := g.azCompare(cl, day, dtName); err != nil {
					return err
				}
				continue
			}

			if dayDate.After(old) {
				continue
			}

			// aggressive mode
			dtPath := filepath.Join(rootDir, day, dtName)
			if g.Verbose {
				log.Printf("Aggressive removal of %v", dtPath)
			}
			if g.Dry {
				continue
			}
			if err := os.RemoveAll(dtPath); err != nil {
				return err
			}
		}

		// so we don't traverse down in its children, since we've already just handled them
		return fs.SkipDir
	}

	return filepath.WalkDir(rootDir, fn)
}

// azCompare checks for each local file in rootDir/dayDir/tpDir if
// their equivalent can be found on Azure, and removes it if yes.
func (g GGC) azCompare(cl *share.Client, dayDir, tpDir string) error {
	rootDir := g.RootDir
	dayCl := cl.NewDirectoryClient(dayDir)
	pager := dayCl.NewListFilesAndDirectoriesPager(nil)
	hasDT := false
	for pager.More() {
		if hasDT {
			break
		}
		resp, err := pager.NextPage(context.Background())
		if err != nil {
			if fileerror.HasCode(err, fileerror.ResourceNotFound) {
				// that day dir not on Azure, all good, abort early
				if g.Verbose {
					log.Printf("No %v day directory on Azure, not removing local copy", dayDir)
				}
				return nil
			}
			return fmt.Errorf("while listing %v: %v", filepath.Join(dayDir, tpDir), err)
		}
		for _, dir := range resp.Segment.Directories {
			if *(dir.Name) != tpDir {
				continue
			}
			hasDT = true
			break
		}
	}
	if !hasDT {
		if g.Verbose {
			log.Printf("No %v/%v directory on Azure, not removing local copy", dayDir, tpDir)
		}
		return nil
	}

	dtCl := dayCl.NewSubdirectoryClient(tpDir)
	pager = dtCl.NewListFilesAndDirectoriesPager(nil)
	var list map[string]int64 = make(map[string]int64)
	for pager.More() {
		resp, err := pager.NextPage(context.Background())
		if err != nil {
			return err
		}
		for _, file := range resp.Segment.Files {
			prop := *(file.Properties)
			list[*(file.Name)] = *(prop.ContentLength)
		}
	}

	tp := filepath.Join(rootDir, dayDir, tpDir)
	ent, err := os.ReadDir(tp)
	if err != nil {
		return fmt.Errorf("while listing local dir %v: %v", tp, err)
	}

	for _, v := range ent {
		if v.IsDir() {
			return fmt.Errorf("there should be no dir in %v", tp)
		}
		name := v.Name()
		fp := filepath.Join(tp, name)
		sz, ok := list[name]
		if !ok {
			if g.Verbose {
				log.Printf("%v not found on Azure, not removing local copy", fp)
			}
			continue
		}
		info, err := v.Info()
		if err != nil {
			return fmt.Errorf("could not get file info for %v : %v", fp, err)
		}
		lsz := info.Size()
		if sz != lsz {
			if g.Verbose {
				log.Printf("%v found on Azure, but different sizes (%d VS %d), so not removing local copy", fp, sz, lsz)
			}
			continue
		}
		log.Printf("%v found on Azure, removing local copy", fp)
		if g.Dry {
			continue
		}
		if err := os.Remove(fp); err != nil {
			return fmt.Errorf("while removing %v: %v", fp, err)
		}
	}

	return nil
}
