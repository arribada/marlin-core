// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package watcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/arribada/marlin-core/collector/pkg/logger"
	"github.com/arribada/marlin-core/collector/pkg/types"
	"github.com/arribada/marlin-core/common/pkg/datatype"
	"golang.org/x/sys/unix"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/directory"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/fileerror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/share"
	"github.com/fsnotify/fsnotify"
	klog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
)

const ComponentName = "data watcher"

type Config struct {
	Enabled bool
	DataDir string
	Verbose bool

	AzureConn  string
	AzureShare string
	// Duration (in seconds) in between upload retries of the full tree of data.
	// Non positive value means only one initial run of uploading the full tree.
	UploadInterval int

	// Duration (in seconds) in between storage space checks. Non positive value
	// means only one initial run.
	DuInterval  int
	DuThreshold float64

	// TODO: do it better
	// VideoAlwaysOn is a way to share knowledge that the video recorder is
	// continuously recording.
	VideoAlwaysOn bool
}

type EventsWatcher struct {
	klogger klog.Logger
	config  Config
	watcher *fsnotify.Watcher

	// should be safe if nil.
	// We can use the same uploader instance for routine uploads in the loop,
	// and for on the fly uploads at file creation time, because the routine uploads
	// are based on .upload files, which are only created after the on the fly upload
	// fails.
	up *uploader

	// should be safe if nil
	du *DuWatcher

	chans *types.Chans

	muVideoRec       sync.RWMutex
	isVideoRecording bool

	// To enforce at least 1s (artificial) interval between each button press,
	// to avoid name conflicts.
	lastButtonPressMu sync.Mutex
	lastButtonPress   time.Time
}

func New(lgr klog.Logger, cfg Config, theChans *types.Chans) (*EventsWatcher, error) {
	if theChans == nil {
		return nil, errors.New("chans not provided")
	}
	lgr, err := logger.ApplyFilter("info", lgr)
	if err != nil {
		return nil, err
	}

	// TODO: do it in a central place (as early as in pkg/config). or in every node?
	if err := os.MkdirAll(cfg.DataDir, os.ModePerm); err != nil {
		return nil, fmt.Errorf("while creating watched dir %v: %w", cfg.DataDir, err)
	}

	wtchr, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	ew := &EventsWatcher{
		klogger: klog.With(lgr, "component", ComponentName),
		config:  cfg,
		watcher: wtchr,
	}

	up, err := newUploader(cfg)
	if err != nil {
		return nil, err
	}
	ew.up = up

	du := NewDuWatcher(cfg)
	ew.du = du

	ew.chans = theChans
	if cfg.VideoAlwaysOn {
		ew.isVideoRecording = true
	}
	go ew.trackVideoRecState()
	go ew.trackButtonPresses()
	return ew, nil
}

func (ew *EventsWatcher) trackVideoRecState() {
	for {
		isRecording := <-ew.chans.VideoState
		if ew.config.VideoAlwaysOn {
			// isVideoRecording already set to true in New.
			continue
		}
		ew.muVideoRec.Lock()
		ew.isVideoRecording = isRecording
		ew.muVideoRec.Unlock()
	}
}

func (ew *EventsWatcher) videoRecState() bool {
	ew.muVideoRec.RLock()
	defer ew.muVideoRec.RUnlock()
	return ew.isVideoRecording
}

func (ew *EventsWatcher) trackButtonPresses() {
	ew.lastButtonPress = time.Now()
	for {
		bp := <-ew.chans.ButtonPress
		// we still want to be responsive for our controller http handler,
		// so we make it all concurrent, to accept button presses as fast
		// as possible.
		go func() {
			// But to avoid file name conflicts, we enforce that each button press
			// is on their own time (at the second granularity), even if the button
			// was pressed more than once within a second.
			ew.lastButtonPressMu.Lock()
			defer ew.lastButtonPressMu.Unlock()
			for {
				if time.Now().Sub(ew.lastButtonPress) > time.Second {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			event := types.Event{
				ButtonPress: &types.ButtonPress{
					Time:  time.Now(),
					Start: bp,
				},
			}
			if err := datatype.WriteEventFile(ew.config.DataDir, event, datatype.ButtonPress); err != nil {
				logger.LogAndSend(fmt.Sprintf("while writing button press event: %v", err))
			}
			ew.lastButtonPress = time.Now()
		}()
	}
}

type DuWatcher struct {
	rootDir   string
	interval  time.Duration
	threshold float64

	Free  uint64
	Total uint64
	Used  float64
}

func NewDuWatcher(cfg Config) *DuWatcher {
	return &DuWatcher{
		rootDir:   cfg.DataDir,
		interval:  time.Duration(cfg.DuInterval) * time.Second,
		threshold: cfg.DuThreshold,
	}
}

func (du *DuWatcher) Update() error {
	if du == nil {
		return nil
	}
	var stat unix.Statfs_t

	if err := unix.Statfs(du.rootDir, &stat); err != nil {
		return err
	}

	du.Free = stat.Bavail * uint64(stat.Bsize)
	du.Total = stat.Blocks * uint64(stat.Bsize)
	du.Used = float64(du.Total-du.Free) / float64(du.Total)

	return nil
}

func (du *DuWatcher) loop(ctx context.Context) {
	if du == nil {
		logger.LogAndSend("Storage space watcher not enabled")
		return
	}

	// Always log storage space once on startup
	if err := du.Update(); err != nil {
		logger.LogAndSend(fmt.Sprintf("while storage watcher updating: %v", err))
	} else {
		logger.LogAndSend(fmt.Sprintf("Storage space usage: %dMB/%dMB used", (du.Total-du.Free)/1e6, du.Total/1e6), true)
	}

	for {
		if err := du.Update(); err != nil {
			logger.LogAndSend(fmt.Sprintf("while storage watcher updating: %v", err))
		}

		if du.Used > du.threshold {
			logger.LogAndSend(fmt.Sprintf("WARNING: Storage space threshold (%4.2f) exceeded: %4.2f. Space left: %dMB", du.threshold, du.Used, du.Free/1e6), true)
		}

		if du.interval <= 0 {
			logger.LogAndSend("Storage watcher loop disabled by interval, only trying once")
			return
		}

		tm := time.NewTimer(du.interval)
		select {
		case <-ctx.Done():
			return
		case <-tm.C:
		}
	}
}

func isSpeech(name string) bool {
	return strings.HasPrefix(name, datatype.PrefixesByType[datatype.Speech]+"-") && strings.HasSuffix(name, "."+datatype.ExtByType[datatype.Speech])
}

func isAudio(name string) bool {
	return strings.HasPrefix(name, datatype.PrefixesByType[datatype.Audio]+"-") && strings.HasSuffix(name, "."+datatype.ExtByType[datatype.Audio])
}

func isVideo(name string) bool {
	return strings.HasPrefix(name, datatype.PrefixesByType[datatype.Video]+"-") && strings.HasSuffix(name, "."+datatype.ExtByType[datatype.Video])
}

func isFishingEvent(name string) bool {
	return strings.HasPrefix(name, datatype.PrefixesByType[datatype.FishingEvent]+"-") && strings.HasSuffix(name, "."+datatype.ExtByType[datatype.FishingEvent])
}

func isGPS(name string) bool {
	return strings.HasPrefix(name, datatype.PrefixesByType[datatype.GPS]+"-") && strings.HasSuffix(name, "."+datatype.ExtByType[datatype.GPS])
}

func isButtonPress(name string) bool {
	return strings.HasPrefix(name, datatype.PrefixesByType[datatype.ButtonPress]+"-") && strings.HasSuffix(name, "."+datatype.ExtByType[datatype.ButtonPress])
}

func (sw *EventsWatcher) Run(ctx context.Context) error {
	go sw.up.loop(ctx)
	go sw.du.loop(ctx)

	logger.LogAndSend("starting to watch " + sw.config.DataDir)

	// N.B: important to add by fullPath here, to make everything after easier.
	if err := sw.watcher.Add(sw.config.DataDir); err != nil {
		return fmt.Errorf("while trying to start watching %v: %v", sw.config.DataDir, err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-sw.watcher.Events:
			if !ok {
				panic("Events chan unexpectedly closed")
			}
			if !event.Has(fsnotify.Create) {
				// not a file creation (nor a rename)
				continue
			}
			sw.handleEvent(event)
		case err, ok := <-sw.watcher.Errors:
			if !ok {
				panic("Errors chan unexpectedly closed")
			}
			logger.LogAndSend(fmt.Sprintf("file watcher error: %v", err))
		}
	}
}

func (ew *EventsWatcher) handleEvent(event fsnotify.Event) {
	name := path.Base(event.Name)
	// should actually be the same as event.Name though
	fullPath := filepath.Join(ew.config.DataDir, name)

	fi, err := os.Stat(fullPath)
	if err != nil {
		logger.LogAndSend(fmt.Sprintf("error while statting file %s: %v", fullPath, err))
		return
	}

	if fi.Mode().IsDir() {
		return
	}

	if isSpeech(name) {
		logger.LogAndSend(fmt.Sprintf("New speech file detected: %s", name))
		go func() {
			if _, err := ew.archiveFile(datatype.Speech, fullPath); err != nil {
				logger.LogAndSend(fmt.Sprintf("error while archiving file %s: %v", fullPath, err))
				return
			}
		}()
		return
	}

	if isAudio(name) {
		logger.LogAndSend(fmt.Sprintf("New audio file detected: %s", name))
		go func() {
			if _, err := ew.archiveFile(datatype.Audio, fullPath); err != nil {
				logger.LogAndSend(fmt.Sprintf("error while archiving file %s: %v", fullPath, err))
				return
			}
		}()
		return
	}

	if isVideo(name) {
		logger.LogAndSend(fmt.Sprintf("New video file detected: %s", name))
		go func() {
			newFullPath, err := ew.archiveFile(datatype.Video, fullPath)
			if err != nil {
				logger.LogAndSend(fmt.Sprintf("error while archiving file %s: %v", fullPath, err), true)
				if newFullPath == "" {
					return
				}
				// we keep going because we still do have a vid file at newFullPath to probe
			}
			if _, err := ProbeVid(newFullPath); err != nil {
				logger.LogAndSend(fmt.Sprintf("error while probing video file %s: %v", newFullPath, err), true)
				return
			}
		}()
		return
	}

	if isGPS(name) {
		_ = level.Debug(ew.klogger).Log("msg", fmt.Sprintf("New gps file detected: %s", name))
		go func() {
			if _, err := ew.archiveFile(datatype.GPS, fullPath); err != nil {
				logger.LogAndSend(fmt.Sprintf("error while archiving file %s: %v", fullPath, err))
				return
			}
		}()
		return
	}

	if isButtonPress(name) {
		logger.LogAndSend(fmt.Sprintf("New button press event file detected: %s", name))
		go func() {
			if _, err := ew.archiveFile(datatype.ButtonPress, fullPath); err != nil {
				logger.LogAndSend(fmt.Sprintf("error while archiving file %s: %v", fullPath, err))
				return
			}
		}()
		return
	}

	log.Println("Unsupported file, doing nothing: ", name)
}

func (sw *EventsWatcher) Stop(cancel context.CancelFunc) {
	_ = level.Info(sw.klogger).Log("msg", "stopped")
	cancel()
	sw.watcher.Close()
}

func (sw *EventsWatcher) archiveFile(dataType int, fullPath string) (string, error) {
	rootDir, name := filepath.Split(fullPath)

	ft, err := datatype.FileTime(dataType, name)
	if err != nil {
		return "", err
	}

	newDir, newName, err := datatype.ArchivedPath(dataType, ft)
	if err != nil {
		return "", err
	}

	if !strings.HasSuffix(fullPath, newName) {
		return "", fmt.Errorf("mismatch between old and new file name: %s VS %s", name, newName)
	}

	newFullDir := filepath.Join(rootDir, newDir)
	if err := os.MkdirAll(newFullDir, 0700); err != nil {
		return "", err
	}

	newFullPath := filepath.Join(newFullDir, newName)
	if err := os.Rename(fullPath, newFullPath); err != nil {
		return "", err
	}

	if err := markForUpload(newFullPath); err != nil {
		return newFullPath, fmt.Errorf("while marking file %v for upload: %v", newFullPath, err)
	}

	return newFullPath, nil
}

const (
	toUploadPrefix = "."
	toUploadSuffix = ".upload"
)

var initUploaderErr = errors.New("uploader not initialized")

func markForUpload(fullPath string) error {
	dir, name := filepath.Split(fullPath)
	markerName := toUpload(name)
	markerPath := filepath.Join(dir, markerName)
	_, err := os.Stat(markerPath)
	if err == nil {
		// marker already there, nothing to do
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	f, err := os.Create(markerPath)
	if err != nil {
		return err
	}
	return f.Close()
}

func toUpload(name string) string {
	return toUploadPrefix + name + toUploadSuffix
}

func isUploadMarker(fileName string) bool {
	return strings.HasPrefix(fileName, toUploadPrefix) && strings.HasSuffix(fileName, toUploadSuffix)
}

type uploader struct {
	azShareCl *share.Client
	rootDir   string
	// time interval in between full traversals of the data tree for upload
	interval time.Duration
	// if specified, skip all days before that one
	startDay string

	// for telemetry report. reset for each full tree run.
	filesUploaded int
	filesSeen     uint64
	filesToUpload int
}

func newUploader(cfg Config) (*uploader, error) {
	if cfg.AzureShare == "" || cfg.AzureConn == "" {
		return nil, nil
	}

	cl, err := share.NewClientFromConnectionString(cfg.AzureConn, cfg.AzureShare, nil)
	if err != nil {
		return nil, fmt.Errorf("Azure share client init failed: %v", err)
	}

	return &uploader{
		azShareCl: cl,
		rootDir:   cfg.DataDir,
		interval:  time.Duration(cfg.UploadInterval) * time.Second,
	}, nil
}

func (up *uploader) loop(ctx context.Context) {
	if up == nil || up.azShareCl == nil {
		logger.LogAndSend("Uploading to Azure not enabled")
		return
	}

	for {
		run := &types.UploadRun{
			TimeStart: time.Now(),
		}
		if err := up.uploadAll(); err != nil {
			logger.LogAndSend(fmt.Sprintf("while the uploader was walking the tree: %v", err), true)
			run.Err = err
		}
		run.TimeEnd = time.Now()
		run.FilesUploaded = up.filesUploaded
		run.FilesSeen = up.filesSeen
		run.FilesToUpload = up.filesToUpload
		// TODO: better report
		log.Printf("upload run report: %v", run)

		// TODO: re-add something to monitor there's no files at the root?

		if up.interval <= 0 {
			logger.LogAndSend("Uploading loop disabled by interval, only trying once")
			return
		}

		tm := time.NewTimer(up.interval)
		select {
		case <-ctx.Done():
			return
		case <-tm.C:
		}
	}
}

func (up *uploader) uploadAll() error {
	if up == nil || up.azShareCl == nil {
		return initUploaderErr
	}

	var skipBefore time.Time
	var err error
	if up.startDay != "" {
		skipBefore, err = time.Parse(time.DateOnly, up.startDay)
		if err != nil {
			return fmt.Errorf("could not parse start day %v as date: %v", up.startDay, err)
		}
	}

	up.filesUploaded = 0
	up.filesToUpload = 0
	up.filesSeen = 0
	isRootDir := true
	fn := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// skip top level dir
		if isRootDir {
			isRootDir = false
			return nil
		}
		if !d.IsDir() {
			return nil
		}

		name := d.Name()
		// TODO: remove when new disk
		if name == "sensordata" {
			return nil
		}
		if name == ".tmp" {
			return nil
		}

		t, err := time.Parse(time.DateOnly, name)
		if err != nil {
			return fmt.Errorf("could not parse day dir %v as date: %v", name, err)
		}
		if !skipBefore.IsZero() {
			if t.Before(skipBefore) {
				return nil
			}
		}

		skipBefore = t
		if err := up.uploadDay(up.rootDir, name); err != nil {
			return err
		}

		// because uploadDay already drilled down the whole subtree
		return filepath.SkipDir
	}

	if err := filepath.WalkDir(up.rootDir, fn); err != nil {
		return err
	}
	return nil
}

func (up *uploader) uploadDay(rootDir, dayDir string) error {
	dayPath := filepath.Join(rootDir, dayDir)
	ent, err := os.ReadDir(dayPath)
	if err != nil {
		return err
	}

	datetimeCl := up.azShareCl.NewDirectoryClient(dayDir)
	if _, err := datetimeCl.Create(context.TODO(), nil); err != nil {
		if !fileerror.HasCode(err, fileerror.ResourceAlreadyExists) {
			return fmt.Errorf("failed to create datetime directory %v: %v", dayDir, err)
		}
	}

	for _, v := range ent {
		if !v.IsDir() {
			continue
		}

		if err := up.uploadType(datetimeCl, dayPath, v.Name()); err != nil {
			return err
		}
	}

	return nil
}

func (up *uploader) uploadType(datetimeCl *directory.Client, dayDir, typeDir string) error {
	if up == nil || up.azShareCl == nil {
		return initUploaderErr
	}

	datatypeCl := datetimeCl.NewSubdirectoryClient(typeDir)
	if _, err := datatypeCl.Create(context.TODO(), nil); err != nil {
		if !fileerror.HasCode(err, fileerror.ResourceAlreadyExists) {
			return fmt.Errorf("failed to create datatype directory %v: %v", typeDir, err)
		}
	}

	typePath := filepath.Join(dayDir, typeDir)
	ent, err := os.ReadDir(typePath)
	if err != nil {
		return err
	}
	for _, v := range ent {
		if v.IsDir() {
			continue
		}
		name := v.Name()

		if !isUploadMarker(name) {
			up.filesSeen++
			continue
		}

		fileToUpload := strings.TrimPrefix(strings.TrimSuffix(name, toUploadSuffix), toUploadPrefix)
		fileToUploadPath := filepath.Join(typePath, fileToUpload)
		if _, err := os.Stat(fileToUploadPath); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			return fmt.Errorf("upload marker file (%s) found, but corresponding file (%s) not found", name, fileToUpload)
		}
		up.filesToUpload++

		if err := up.uploadFile(datatypeCl, typePath, fileToUpload); err != nil {
			return fmt.Errorf("while trying to upload %v: %v", fileToUploadPath, err)
		}
		up.filesUploaded++

		markerPath := filepath.Join(typePath, name)
		if err := os.Remove(markerPath); err != nil {
			return fmt.Errorf("failed to remove upload marker %v: %v", markerPath, err)
		}
	}

	return nil
}

func (up *uploader) uploadFile(datatypeCl *directory.Client, typeDir, filename string) error {
	fullPath := filepath.Join(typeDir, filename)
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

	// TODO: should we validate more strictly, i.e. by common datatype?
	if strings.HasSuffix(filename, ".") {
		// skip hidden files, notably the .DS_Store bs
		return nil
	}

	parts := strings.Split(typeDir, "/")
	nb := len(parts)
	if nb < 3 {
		return fmt.Errorf("unexpected number of parts in type dir: %d", len(parts))
	}
	dayDir, tpDir := parts[nb-2], parts[nb-1]
	tempFilename := filename + ".part"
	fClient := datatypeCl.NewFileClient(tempFilename)
	if _, err := fClient.Create(context.TODO(), fSize, nil); err != nil {
		return fmt.Errorf("failed to create file %v: %v", tempFilename, err)
	}

	if err := fClient.UploadFile(context.TODO(), f, nil); err != nil {
		return fmt.Errorf("failed to upload contents for file %v: %v", tempFilename, err)
	}

	rnPath := filepath.Join(dayDir, tpDir, filename)
	if _, err := fClient.Rename(context.TODO(), rnPath, nil); err != nil {
		return fmt.Errorf("failed to rename %v into %v: %v", tempFilename, filename, err)
	}

	logger.LogAndSend(fmt.Sprintf("Successfully uploaded to Azure: %v", fullPath))
	return nil
}

// should be a const, but actually a var so we can change it during tests
var FFprobebin string

func init() {
	// location in production on gokrazy
	FFprobebin = "/usr/local/bin/ffprobe"
	loc, err := exec.LookPath("ffprobe")
	if err == nil {
		FFprobebin = loc
		return
	}
}

func ProbeVid(fullPath string) (string, error) {
	var stderr bytes.Buffer
	var stdout bytes.Buffer
	cmd := exec.Command(FFprobebin, "-hide_banner", fullPath)
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	err := cmd.Run()
	out := stdout.String() + stderr.String()
	if err != nil {
		return "", fmt.Errorf("while probing %v: %v", fullPath, out)
	}
	return out, nil
}
