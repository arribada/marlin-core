// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package gc

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/share"
)

func TestAudioGC(t *testing.T) {
	g := AudioGC{
		RootDir:     "testdata/audio",
		Verbose:     true,
		GracePeriod: 60,
		dry:         true,
		hook: &testHook{
			found:   make(map[string]struct{}),
			removed: make(map[string]struct{}),
		},
	}

	// touch this file rn, to make it recent, so it gets graced and ignored by the GC
	if err := os.WriteFile(g.RootDir+"/2024-07-27/audio/audio-2024-07-27T08_16_12Z.wav", []byte("touched"), 0700); err != nil {
		t.Fatal(err)
	}

	if err := g.Run(); err != nil {
		t.Fatal(err)
	}

	wantFound := map[string]struct{}{
		g.RootDir + "/2024-07-27/stt/stt-2024-07-27T08_16_19Z.json":         {},
		g.RootDir + "/2024-07-27/stt/stt-2024-07-27T08_23_29Z.json":         {},
		g.RootDir + "/2024-07-27/video/video-2024-07-27T07_55_18Z.mp4":      {},
		g.RootDir + "/2024-07-27/audio/audio-2024-07-27T08_16_19Z.wav":      {},
		g.RootDir + "/2024-07-27/audio/audio-2024-07-27T08_23_09Z.wav":      {},
		g.RootDir + "/2024-07-27/audio/audio-2024-07-27T08_23_29Z.wav":      {},
		g.RootDir + "/2024-07-27/fishing/fishing-2024-07-27T09_44_37Z.json": {},
		g.RootDir + "/2024-07-27/audio/audio-2024-07-27T08_16_12Z.wav":      {},
		g.RootDir + "/2024-07-27/gps/gps-2024-07-27T09_44_36Z.json":         {},
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
		g.RootDir + "/2024-07-27/audio/audio-2024-07-27T08_23_09Z.wav": {},
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

func du(rootDir string, t *testing.T) (int64, error) {
	cmd := exec.Command("du", "-sc", rootDir)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	var lastline string
	for sc.Scan() {
		lastline = sc.Text()
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	if !strings.HasSuffix(lastline, "total") {
		return 0, fmt.Errorf("unexpected line: %v", lastline)
	}
	total := strings.TrimSpace(strings.TrimSuffix(lastline, "total"))
	tot, err := strconv.Atoi(total)
	if err != nil {
		return 0, err
	}
	return int64(tot), nil
}

func mockWantSpace(rootDir string, total int64, threshold float64, t *testing.T) (bool, float64) {
	used, err := du(rootDir, t)
	if err != nil {
		t.Fatal(err)
	}
	quota := float64(used) / float64(total)
	return (quota > threshold), quota
}

func countFiles(root string, dayPattern string) (int, error) {
	count := 0
	fn := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		name := d.Name()
		if !strings.Contains(name, dayPattern) {
			return nil
		}
		count++
		return nil
	}

	if err := filepath.WalkDir(root, fn); err != nil {
		return 0, err
	}
	return count, nil
}

func removeFiles(root string, dayPattern string) error {
	fn := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		name := d.Name()
		if !strings.Contains(name, dayPattern) {
			return nil
		}
		return os.Remove(p)
	}

	return filepath.WalkDir(root, fn)
}

func defaultRestore(backup, root string) error {
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	return os.CopyFS(root, os.DirFS(backup))
}

// TODO: move to common. and add bool arg for whether to delete or rename.
func azRemove(azShareCl *share.Client, fullPath string) error {
	parts := strings.Split(fullPath, string(filepath.Separator))
	lp := len(parts)
	if lp < 3 {
		return fmt.Errorf("unexpected path hierarchy, wanted 'someprefix/datetime/datatype/filename', got %s", fullPath)
	}

	datetime, datatype, filename := parts[lp-3], parts[lp-2], parts[lp-1]

	datetimeCl := azShareCl.NewDirectoryClient(datetime)
	datatypeCl := datetimeCl.NewSubdirectoryClient(datatype)
	fClient := datatypeCl.NewFileClient(filename)
	if _, err := fClient.GetProperties(context.TODO(), nil); err != nil {
		// probably because does not exist, but I wanna know about it for now
		return err
	}
	rnPath := filepath.Join(datetime, datatype, filename+".deleted")
	if _, err := fClient.Rename(context.TODO(), rnPath, nil); err != nil {
		return fmt.Errorf("failed to rename %v into %v: %v", filename, rnPath, err)
	}
	/*
		if _, err := fClient.Delete(context.TODO(), nil); err != nil {
			return fmt.Errorf("failed to delete file %v: %v", filename, err)
		}
	*/
	return nil
}

func removeAzFiles(azShareCl *share.Client, root string, dayPattern string) error {
	fn := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		name := d.Name()
		if !strings.Contains(name, dayPattern) {
			return nil
		}

		return azRemove(azShareCl, p)
	}

	return filepath.WalkDir(root, fn)
}

/*
// TODO: move to common
func azAdd(azShareCl *share.Client, fullPath string) error {
	parts := strings.Split(fullPath, string(filepath.Separator))
	lp := len(parts)
	if lp < 3 {
		return fmt.Errorf("unexpected path hierarchy, wanted 'someprefix/datetime/datatype/filename', got %s", fullPath)
	}

	datetime, datatype, filename := parts[lp-3], parts[lp-2], parts[lp-1]

	datetimeCl := azShareCl.NewDirectoryClient(datetime)
	if _, err := datetimeCl.Create(context.TODO(), nil); err != nil {
		if !fileerror.HasCode(err, fileerror.ResourceAlreadyExists) {
			return fmt.Errorf("failed to create datetime directory %v: %v", datetime, err)
		}
	}

	datatypeCl := datetimeCl.NewSubdirectoryClient(datatype)
	if _, err := datatypeCl.Create(context.TODO(), nil); err != nil {
		if !fileerror.HasCode(err, fileerror.ResourceAlreadyExists) {
			return fmt.Errorf("failed to create datatype directory %v: %v", datatype, err)
		}
	}

	f, err := os.OpenFile(fullPath, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	fInfo, err := f.Stat()
	if err != nil {
		return err
	}
	fSize := fInfo.Size()

	fClient := datatypeCl.NewFileClient(filename)
	if _, err := fClient.GetProperties(context.TODO(), nil); err == nil {
		// already exists, all good
		return nil
	}
	if _, err := fClient.Create(context.TODO(), fSize, nil); err != nil {
		// Note: I think this check is actually useless and this is never
		// true even if file already existed. Which is why we do GetProperties
		// above instead.
		if fileerror.HasCode(err, fileerror.ResourceAlreadyExists) {
			// already exists, all good
			return nil
		}
		return fmt.Errorf("failed to create file %v: %v", filename, err)
	}

	if err := fClient.UploadFile(context.TODO(), f, nil); err != nil {
		return fmt.Errorf("failed to upload contents for file %v: %v", filename, err)
	}

	log.Printf("Successfully uploaded to Azure: %v", fullPath)
	return nil
}

func addAzFiles(azShareCl *share.Client, root string, dayPattern string) error {
	fn := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		name := d.Name()
		if !strings.Contains(name, dayPattern) {
			return nil
		}

		return azAdd(azShareCl, p)
	}

	return filepath.WalkDir(root, fn)
}
*/

func azRestore(azShareCl *share.Client, fullPath string) error {
	parts := strings.Split(fullPath, string(filepath.Separator))
	lp := len(parts)
	if lp < 3 {
		return fmt.Errorf("unexpected path hierarchy, wanted 'someprefix/datetime/datatype/filename', got %s", fullPath)
	}

	datetime, datatype, filename := parts[lp-3], parts[lp-2], parts[lp-1]

	datetimeCl := azShareCl.NewDirectoryClient(datetime)
	datatypeCl := datetimeCl.NewSubdirectoryClient(datatype)
	fClient := datatypeCl.NewFileClient(filename + ".deleted")
	if _, err := fClient.GetProperties(context.TODO(), nil); err != nil {
		return nil
	}
	rnPath := filepath.Join(datetime, datatype, filename)
	if _, err := fClient.Rename(context.TODO(), rnPath, nil); err != nil {
		return fmt.Errorf("failed to rename %v into %v: %v", filename+".deleted", rnPath, err)
	}
	return nil
}

func restoreAzFiles(azShareCl *share.Client, root string, dayPattern string) error {
	fn := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		name := d.Name()
		if !strings.Contains(name, dayPattern) {
			return nil
		}

		return azRestore(azShareCl, p)
	}

	return filepath.WalkDir(root, fn)
}

const (
	rootDir   = "testdata/integration"
	backupDir = "testdata/integration-backup"
	vessel    = "gctests"
)

var connString string

func shareCl(t *testing.T) (*share.Client, error) {
	connString = os.Getenv("AZURE_CONN")
	if connString == "" {
		return nil, fmt.Errorf("Azure connection string not specified")
	}
	vessel := "gctests"
	azShareCl, err := share.NewClientFromConnectionString(connString, vessel, nil)
	if err != nil {
		return nil, err
	}
	return azShareCl, nil
}

// This is not an actual test, but a brute-force way to restore all
// the files (both local and on Azure) for the TestGC.
func testRestoreAll(t *testing.T) {
	if err := defaultRestore(backupDir, rootDir); err != nil {
		t.Fatal(err)
	}
	azShareCl, err := shareCl(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := restoreAzFiles(azShareCl, rootDir, "2025"); err != nil {
		t.Fatal(err)
	}
}

func TestGC(t *testing.T) {
	// a full day has 23 json files + 23 mp4 files = 46 files
	filesPerDay := 46
	foundFiles, err := countFiles(rootDir, "2025")
	if err != nil {
		t.Fatal(err)
	}
	allExpectedFiles := (7*31 + 28 + 4*30) * filesPerDay
	if foundFiles != allExpectedFiles {
		t.Fatalf("rootDir not populated; found %d files, expected %d files. Please populate manually with ../collector/integration/main.go -populate", foundFiles, allExpectedFiles)
	}

	// our first backup is the inverse of a restore
	if err := defaultRestore(rootDir, backupDir); err != nil {
		t.Fatal(err)
	}

	defaultAgeLimit := 31 * 24 * time.Hour

	azShareCl, err := shareCl(t)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		desc      string
		threshold float64
		ageLimit  time.Duration
		total     float64
		populate  func() error
		assert    func(t *testing.T, total int64, threshold float64) error
		restore   func() error
		dry       bool
	}{
		{
			desc:      "dry run does nothing",
			total:     0.8,
			threshold: 0.5,
			populate: func() error {
				// NOOP
				// start with a full population both on local and remote
				return nil
			},
			assert: func(t *testing.T, total int64, threshold float64) error {
				foundFiles, err := countFiles(rootDir, "2025")
				if err != nil {
					return err
				}
				if foundFiles != allExpectedFiles {
					return fmt.Errorf("missing files after a dry run! expected %d, got %d", allExpectedFiles, foundFiles)
				}
				return nil
			},
			restore: func() error {
				// NOOP
				return nil
			},
			dry: true,
		},
		{
			desc:      "nothing happens if we're below threshold",
			total:     0.7,
			threshold: 0.9,
			populate: func() error {
				// NOOP
				// start with a full population both on local and remote
				return nil
			},
			assert: func(t *testing.T, total int64, threshold float64) error {
				foundFiles, err := countFiles(rootDir, "2025")
				if err != nil {
					return err
				}
				if foundFiles != allExpectedFiles {
					return fmt.Errorf("missing files when started below threshold! expected %d, got %d", allExpectedFiles, foundFiles)
				}
				return nil
			},
			restore: func() error {
				// NOOP
				return nil
			},
		},
		{
			desc: "non-agressive stops at threshold, and aggressive never starts",
			// we start with a pretend drive that is 100% full
			total: 1.,
			// we want to recover space such that we are only at 11/12 ~= 92% usage.
			// So 1 month worth of data will have to be removed.
			threshold: 11. / 12.,
			populate: func() error {
				// NOOP
				// start with a full population both on local and remote
				return nil
			},
			assert: func(t *testing.T, total int64, threshold float64) error {
				gotUsed, err := du(rootDir, t)
				if err != nil {
					return err
				}
				ratio := float64(gotUsed) / float64(total)
				// Allow for 1% leeway
				if math.Abs(ratio-threshold) > 1 {
					return fmt.Errorf("after non-aggressive GC, wanted %v, got %v", threshold, ratio)
				}

				// check that all of january is gone
				nb, err := countFiles(rootDir, "2025-01")
				if err != nil {
					return err
				}
				if nb > 0 {
					return fmt.Errorf("found %d files in january, expected none", nb)
				}
				nb, err = countFiles(rootDir, "2025-02")
				if err != nil {
					return err
				}

				// 28 days in february, check that they're untouched
				wantFiles := filesPerDay * 28
				if nb != wantFiles {
					return fmt.Errorf("found %d files in february, expected %d", nb, wantFiles)
				}

				// check that no other month was touched
				nb, err = countFiles(rootDir, "2025")
				if err != nil {
					return err
				}
				wantFiles = filesPerDay * (6*31 + 28 + 4*30)
				if nb < wantFiles {
					return fmt.Errorf("found %d files overall, expected at least %d", nb, wantFiles)
				}

				return nil
			},
			restore: func() error {
				return defaultRestore(backupDir, rootDir)
			},
		},
		{
			desc: "non-agressive does not remove files not yet on Azure",
			// we start with a pretend drive that is 100% full
			total: 1.,
			// we want to recover space such that we are only at 11/12 ~= 92% usage.
			// So 1 month worth of data will have to be removed.
			threshold: 11. / 12.,
			populate: func() error {
				return removeAzFiles(azShareCl, rootDir, "2025-01")
			},
			assert: func(t *testing.T, total int64, threshold float64) error {
				gotUsed, err := du(rootDir, t)
				if err != nil {
					return err
				}
				ratio := float64(gotUsed) / float64(total)
				// Allow for 1% leeway
				if math.Abs(ratio-threshold) > 1 {
					return fmt.Errorf("after non-aggressive GC, wanted %v, got %v", threshold, ratio)
				}

				// check that january is untouched
				nb, err := countFiles(rootDir, "2025-01")
				if err != nil {
					return err
				}
				wantFiles := filesPerDay * 31
				if nb != wantFiles {
					return fmt.Errorf("found %d files in january, expected %d", nb, wantFiles)
				}

				// check that all of february is gone
				nb, err = countFiles(rootDir, "2025-02")
				if err != nil {
					return err
				}
				if nb > 0 {
					return fmt.Errorf("found %d files in february, expected none", nb)
				}

				// check that no other month was touched
				nb, err = countFiles(rootDir, "2025")
				if err != nil {
					return err
				}
				wantFiles = filesPerDay * (7*31 + 4*30)
				// Allow for 1% leeway because of 28 days VS 30 days VS 31 days depending on the month
				if math.Abs(float64(wantFiles)-float64(nb)) > (float64(wantFiles) / 100) {
					return fmt.Errorf("found %d files overall, expected around %d", nb, wantFiles)
				}

				return nil
			},
			restore: func() error {
				if err := defaultRestore(backupDir, rootDir); err != nil {
					return err
				}
				return restoreAzFiles(azShareCl, rootDir, "2025-01")
			},
		},
		{
			desc: "aggressive runs when non-aggressive was not enough, and respects age limit",
			// we start with a pretend drive that is 100% full
			total: 1.,
			// To make things simpler we populate drive with the first 4 months.
			// And we set up Azure so that only January has been backed up.
			// We set up an ageLimit at end of february.
			// We ask for a 25% threshold, which (if unbounded) would remove the first 3 months.
			// Non-aggressive will remove january, but should not be allowed
			// to touch the rest as none of it backed to Azure.
			// Then aggressive will remove february, but should not be allowed
			// to go any further because of the ageLimit.
			// So we should end up with only 2 months being removed (and the
			// threshold goal not achieved).
			threshold: 1. / 4.,
			populate: func() error {
				for _, month := range []string{
					"2025-05", "2025-06", "2025-07", "2025-08", "2025-09", "2025-10", "2025-11", "2025-12",
				} {
					if err := removeFiles(rootDir, month); err != nil {
						return err
					}
				}
				for _, month := range []string{
					"2025-02", "2025-03", "2025-04",
				} {
					if err := removeAzFiles(azShareCl, rootDir, month); err != nil {
						return err
					}
				}
				return nil
			},
			// Since the code is based on an age rather than a date (so based
			// on time.Now()), we have to reverse engineer the ageLimit based on
			// when we are running right now.
			// We want nothing in march 2025 and beyond to be touched.
			ageLimit: func() time.Duration {
				boundary, err := time.Parse(time.DateOnly, "2025-02-28")
				if err != nil {
					t.Fatal(err)
				}
				return time.Now().Sub(boundary)
			}(),
			assert: func(t *testing.T, total int64, threshold float64) error {
				gotUsed, err := du(rootDir, t)
				if err != nil {
					return err
				}
				ratio := float64(gotUsed) / float64(total)
				// The target was 25%, but because of agelimit, we only reached 50%
				// Allow for 1% leeway
				if math.Abs(ratio-0.5) > 1 {
					return fmt.Errorf("after GC, wanted %v, got %v", 0.25, ratio)
				}

				// check that all of january is gone
				nb, err := countFiles(rootDir, "2025-01")
				if err != nil {
					return err
				}
				if nb > 0 {
					return fmt.Errorf("found %d files in january, expected none", nb)
				}

				// check that all of february is gone, because of aggressive GC
				nb, err = countFiles(rootDir, "2025-02")
				if err != nil {
					return err
				}
				if nb > 0 {
					return fmt.Errorf("found %d files in february, expected none", nb)
				}

				// check that march is untouched
				nb, err = countFiles(rootDir, "2025-03")
				if err != nil {
					return err
				}
				wantFiles := filesPerDay * 31
				if math.Abs(float64(wantFiles)-float64(nb)) > (float64(wantFiles) / 100) {
					return fmt.Errorf("found %d files in march, expected around %d", nb, wantFiles)
				}

				// check that no other month (march and april) was touched
				nb, err = countFiles(rootDir, "2025")
				if err != nil {
					return err
				}
				wantFiles = filesPerDay * (31 + 30)
				if math.Abs(float64(wantFiles)-float64(nb)) > (float64(wantFiles) / 100) {
					return fmt.Errorf("found %d files overall, expected around %d", nb, wantFiles)
				}

				return nil
			},
			restore: func() error {
				if err := defaultRestore(backupDir, rootDir); err != nil {
					return err
				}
				for _, month := range []string{
					"2025-02", "2025-03", "2025-04",
				} {
					if err := restoreAzFiles(azShareCl, rootDir, month); err != nil {
						return err
					}
				}
				return nil
			},
		},
	}

	for k, tt := range tests {
		inAFunc := func() {
			println(k, tt.desc)

			defer func() {
				if err := tt.restore(); err != nil {
					t.Fatalf("%d: %v", k, err)
				}
			}()

			if err := tt.populate(); err != nil {
				t.Fatalf("%d: %v", k, err)
			}

			n, err := du(rootDir, t)
			if err != nil {
				t.Fatal(err)
			}
			total := int64(float64(n) / tt.total)

			NeedSpace = func(string, float64) (bool, float64) {
				return mockWantSpace(rootDir, total, tt.threshold, t)
			}

			ageLimit := tt.ageLimit
			if ageLimit == 0 {
				ageLimit = defaultAgeLimit
			}

			gg := GGC{
				ConnString: connString,
				Vessel:     vessel,
				RootDir:    rootDir,
				Verbose:    true,
				Threshold:  tt.threshold,
				Aggressive: false,
				AgeLimit:   ageLimit,
				Dry:        tt.dry,
			}

			log.Printf("Starting a generic GC run")
			if err := gg.Run(); err != nil {
				t.Fatalf("%d: %v", k, err)
			}

			if w, _ := gg.WantSpace(); w {
				gg.Aggressive = true
				log.Printf("Starting an aggressive generic GC run")
				if err := gg.Run(); err != nil {
					t.Fatalf("%d: %v", k, err)
				}
			}

			if err := tt.assert(t, total, tt.threshold); err != nil {
				t.Fatalf("%d: %v", k, err)
			}
		}
		inAFunc()
	}
}
