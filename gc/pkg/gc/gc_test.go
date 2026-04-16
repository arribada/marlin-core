// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package gc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io/fs"
	"log"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/share"
)

// Used for manual syncing/debugging.
func TestSyncAzure(t *testing.T) {
	return
	syncAllToAzure(rootDir, t)
}

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

const (
	rootDir   = "testdata/integration"
	backupDir = "testdata/integration-backup"
	vessel    = "gctests"
	envFile   = "azure.env"
)

var (
	sasToken   string
	connString string
	azShareCl  *share.Client
)

func init() {
	data, _ := os.ReadFile(envFile)
	sasToken = strings.TrimPrefix(string(data), "SAS_TOKEN=")
	connString = `BlobEndpoint=https://i360ng.blob.core.windows.net/;QueueEndpoint=https://i360ng.queue.core.windows.net/;FileEndpoint=https://i360ng.file.core.windows.net/;TableEndpoint=https://i360ng.table.core.windows.net/;SharedAccessSignature=` + sasToken
}

func du(rootDir string) (int64, error) {
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
	used, err := du(rootDir)
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
func azRemove(cl *share.Client, fullPath string) error {
	parts := strings.Split(fullPath, string(filepath.Separator))
	lp := len(parts)
	if lp < 3 {
		return fmt.Errorf("unexpected path hierarchy, wanted 'someprefix/datetime/datatype/filename', got %s", fullPath)
	}

	datetime, datatype, filename := parts[lp-3], parts[lp-2], parts[lp-1]

	datetimeCl := cl.NewDirectoryClient(datetime)
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
	return nil
}

func removeAzFiles(cl *share.Client, root string, dayPattern string) error {
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

		return azRemove(cl, p)
	}

	return filepath.WalkDir(root, fn)
}

func azRestore(cl *share.Client, fullPath string) error {
	parts := strings.Split(fullPath, string(filepath.Separator))
	lp := len(parts)
	if lp < 3 {
		return fmt.Errorf("unexpected path hierarchy, wanted 'someprefix/datetime/datatype/filename', got %s", fullPath)
	}

	datetime, datatype, filename := parts[lp-3], parts[lp-2], parts[lp-1]

	datetimeCl := cl.NewDirectoryClient(datetime)
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

func restoreAzFiles(cl *share.Client, root string, dayPattern string) error {
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

		return azRestore(cl, p)
	}

	return filepath.WalkDir(root, fn)
}

func shareCl(t *testing.T) (*share.Client, error) {
	if connString == "" {
		t.Skipf("Azure connection string not specified")
	}
	vessel := "gctests"
	cl, err := share.NewClientFromConnectionString(connString, vessel, nil)
	if err != nil {
		return nil, err
	}
	return cl, nil
}

func syncAllToAzure(dir string, t *testing.T) {
	if sasToken == "" {
		t.Skipf("Azure connection string not specified")
	}

	pattern := dir + "/*"
	if err := azCopy(pattern, ""); err != nil {
		t.Fatal(err)
	}
}

func azCopy(local, remote string) error {
	log.Printf("uploading %v", local)
	cmd := exec.Command("azcopy", "cp", local,
		fmt.Sprintf("https://i360ng.file.core.windows.net/gctests%s?%s", remote, sasToken),
		"--recursive=true", `--exclude-pattern="*.DS_Store"`)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func restoreAll(t *testing.T) {
	log.Printf("trying to restore from backup dir")
	if err := os.RemoveAll(rootDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := defaultRestore(backupDir, rootDir); err != nil {
		log.Printf("failed to restore from backup dir: %v", err)
		log.Printf("trying to restore with automatic population")
		// TODO: first wipe rootDir?
		if err := genData(rootDir); err != nil {
			t.Fatalf("failed to populate rootDir: %v", err)
		}
	}

	syncAllToAzure(rootDir, t)
}

type gcTest struct {
	desc      string
	threshold float64
	ageLimit  time.Duration
	total     float64
	populate  func() error
	assert    func(t *testing.T, total int64, threshold float64) error
	restore   func() error
	dry       bool
}

// making them all distinct tests so we don't have to set the timeout over 10 mins for go test
var gcTests = []gcTest{
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
			gotUsed, err := du(rootDir)
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
			gotUsed, err := du(rootDir)
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
				panic(err)
			}
			return time.Now().Sub(boundary)
		}(),
		assert: func(t *testing.T, total int64, threshold float64) error {
			gotUsed, err := du(rootDir)
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

func TestGC0(t *testing.T) {
	testGC(t, 0)
}

func TestGC1(t *testing.T) {
	testGC(t, 1)
}

func TestGC2(t *testing.T) {
	testGC(t, 2)
}

func TestGC3(t *testing.T) {
	testGC(t, 3)
}

func TestGC4(t *testing.T) {
	testGC(t, 4)
}

var (
	// a full day has 23 json files + 23 mp4 files = 46 files
	filesPerDay      = 46
	allExpectedFiles = (7*31 + 28 + 4*30) * filesPerDay
)

func testGC(t *testing.T, k int) {
	tt := gcTests[k]

	foundFiles, err := countFiles(rootDir, "2025")
	if err != nil {
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if foundFiles != allExpectedFiles {
		log.Printf("rootDir not populated; found %d files, expected %d files", foundFiles, allExpectedFiles)
		restoreAll(t)
	}

	// our first backup is the inverse of a restore
	if err := defaultRestore(rootDir, backupDir); err != nil {
		t.Fatal(err)
	}

	defaultAgeLimit := 31 * 24 * time.Hour

	azShareCl, err = shareCl(t)
	if err != nil {
		t.Fatal(err)
	}

	println(k, ":", tt.desc)

	defer func() {
		if err := tt.restore(); err != nil {
			t.Fatalf("%d: %v", k, err)
		}
	}()

	if err := tt.populate(); err != nil {
		t.Fatalf("%d: %v", k, err)
	}

	n, err := du(rootDir)
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
		//		Verbose:    testing.Verbose(),
		Verbose:    false,
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

// copied and modified from ../collector/integration. See later if we want to refactor all that.
func genData(theRootDir string) error {
	vidData, err := initVid()
	if err != nil {
		return err
	}

	gpsData, err := initGPS()
	if err != nil {
		return err
	}

	day, err := time.Parse(time.DateOnly, "2025-01-01")
	if err != nil {
		return err
	}
	endDay, err := time.Parse(time.DateOnly, "2025-12-31")
	if err != nil {
		return err
	}

	for {
		if day.After(endDay) {
			break
		}
		dayName := day.Format(time.DateOnly)
		dayDir := filepath.Join(theRootDir, dayName)
		// TODO: use common.DataTypes
		for _, dt := range []string{"video", "gps"} {
			dtDir := filepath.Join(dayDir, dt)
			if err := os.MkdirAll(dtDir, 0755); err != nil {
				return err
			}
			t := day.Add(time.Second)
			for i := 0; i < 23; i++ {
				name := t.Format(time.RFC3339)
				name = strings.Replace(name, ":", "_", -1)
				name = dt + "-" + name
				var data []byte
				if dt == "gps" {
					name = name + ".json"
					data = gpsData
				}
				if dt == "video" {
					name = name + ".mp4"
					data = vidData
				}
				fullPath := filepath.Join(dtDir, name)
				if _, err := os.Stat(fullPath); err != nil {
					if !os.IsNotExist(err) {
						return err
					}
					if err := os.WriteFile(fullPath, data, 0755); err != nil {
						return err
					}
				}
				t = t.Add(time.Hour)
			}
		}
		day = day.Add(24 * time.Hour)
	}
	return nil
}

func makeData(size int) ([]byte, error) {
	max := big.NewInt(255)
	data := make([]byte, size)
	for i := range data {
		val, err := rand.Int(rand.Reader, max)
		if err != nil {
			return nil, err
		}
		v := byte(val.Int64())
		data[i] = v
	}
	return data, nil
}

func initVid() ([]byte, error) {
	// 2GB spread on 1 year. 23 videos each day.
	// -> ~240K per vid
	// Maybe add a bit of randomization on that number later.
	size := int(2e9) / 365
	size = int(size / 23)
	return makeData(size)
}

func initGPS() ([]byte, error) {
	return makeData(1e3)
}
