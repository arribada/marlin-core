// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package watcher

import (
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
	"github.com/arribada/marlin-core/collector/pkg/node/watcher/gps"
	"github.com/arribada/marlin-core/collector/pkg/types"
	"github.com/arribada/marlin-core/common/pkg/datatype"
	"github.com/arribada/marlin-core/common/pkg/utils"
	"golang.org/x/sys/unix"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/file"
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
	GPS     *gps.Config `json:",omitempty"`

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

	// disabled if nil.
	gps *gps.GPSEvents

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

	muBP sync.RWMutex
	bp   []*types.ButtonPress

	muEvSeen sync.RWMutex
	evSeen   map[string]bool
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
	ew.evSeen = make(map[string]bool)

	du := NewDuWatcher(cfg)
	ew.du = du

	// TODO: this check does not work, see TODO in Config type.
	if cfg.GPS != nil {
		g, err := gps.NewGPSEvents(lgr, *cfg.GPS)
		if err != nil {
			return nil, err
		}
		ew.gps = g
	}

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
	for {
		bp := <-ew.chans.ButtonPress
		go func() {
			event := types.Event{
				ButtonPress: &types.ButtonPress{
					Time:  time.Now(),
					Start: bp,
				},
			}
			if err := datatype.WriteEventFile(ew.config.DataDir, event, datatype.ButtonPress); err != nil {
				logger.LogAndSend(fmt.Sprintf("while writing button press event: %v", err))
				return
			}
			ew.muBP.Lock()
			defer ew.muBP.Unlock()
			ew.bp = append(ew.bp, event.ButtonPress)
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

func isSensorData(name string) bool {
	return strings.HasPrefix(name, datatype.PrefixesByType[datatype.SensorData]+"-") && strings.HasSuffix(name, "."+datatype.ExtByType[datatype.SensorData])
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
				if err == dupArchiving {
					return
				}
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
				if err == dupArchiving {
					return
				}
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
				if err == dupArchiving {
					return
				}
				logger.LogAndSend(fmt.Sprintf("error while archiving file %s: %v", fullPath, err), true)
				if newFullPath == "" {
					return
				}
				// we keep going because we still do have a vid file at newFullPath to probe
			}
			if err := probeVid(newFullPath); err != nil {
				logger.LogAndSend(fmt.Sprintf("error while probing video file %s: %v", newFullPath, err), true)
				return
			}
		}()
		return
	}

	if isFishingEvent(name) {
		logger.LogAndSend(fmt.Sprintf("New fishing event file detected: %s", name))
		go func() {
			if _, err := ew.archiveFile(datatype.FishingEvent, fullPath); err != nil {
				if err == dupArchiving {
					return
				}
				logger.LogAndSend(fmt.Sprintf("error while archiving file %s: %v", fullPath, err))
				return
			}
		}()
		return
	}

	if isGPS(name) {
		_ = level.Debug(ew.klogger).Log("msg", fmt.Sprintf("New gps file detected: %s", name))
		go func() {
			// TODO: we rely on internal mutex so far. probably good enough, but if there's
			// too much work (goroutine creation above piling up), maybe pile work on with a
			// chan instead.
			_, err := ew.gps.AddEventsFile(fullPath)
			if err != nil {
				logger.LogAndSend(err.Error())
				// TODO: maybe don't skip archiving though? or not always?
				return
			}
			ew.muBP.Lock()
			ew.bp = nil
			ew.muBP.Unlock()
			if _, err := ew.archiveFile(datatype.GPS, fullPath); err != nil {
				if err == dupArchiving {
					return
				}
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
				if err == dupArchiving {
					return
				}
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

var dupArchiving = errors.New("duplicate archiving")

func (sw *EventsWatcher) archiveFile(dataType int, fullPath string) (string, error) {
	// TODO: we should do that up in the caller, as we're doing other
	// things before/after archiveFile for some kinds of data. But good
	// enough to debug/see the race for now.
	// Also, maybe one lock per kind of dataType?
	sw.muEvSeen.RLock()
	_, ok := sw.evSeen[fullPath]
	sw.muEvSeen.RUnlock()
	if ok {
		logger.LogAndSend(fmt.Sprintf("duplicate archiving detected for %v", fullPath))
		return "", dupArchiving
	}

	sw.muEvSeen.Lock()
	_, ok = sw.evSeen[fullPath]
	if ok {
		sw.muEvSeen.Unlock()
		logger.LogAndSend(fmt.Sprintf("duplicate archiving detected for %v", fullPath))
		return "", dupArchiving
	}
	sw.evSeen[fullPath] = true
	sw.muEvSeen.Unlock()
	defer func() {
		sw.muEvSeen.Lock()
		delete(sw.evSeen, fullPath)
		sw.muEvSeen.Unlock()
	}()

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

	if err := sw.up.syncToAzure(newFullPath); err != nil {
		if err == initUploaderErr {
			// no need to log about it, Azure uploading is just not enabled.
			// But we still wanna mark for later upload though.
			if err := markForUpload(newFullPath); err != nil {
				return newFullPath, fmt.Errorf("while marking file %v for upload: %v", newFullPath, err)
			}
			return newFullPath, nil
		}
		if mkErr := markForUpload(newFullPath); mkErr != nil {
			return newFullPath, utils.MaybeWrap(mkErr, err, fmt.Sprintf("while marking file %v for upload", newFullPath))
		}
		// uploads are expected to fail when there's no network, so we only log about it
		logger.LogAndSend(fmt.Sprintf("error while uploading on the fly to Azure: %v", err))
		return newFullPath, nil
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
	interval  time.Duration

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

		if err := up.uploadRoot(); err != nil {
			logger.LogAndSend(fmt.Sprintf("while uploading the files at the root: %v", err), true)
		}
		if up.filesSeen != 0 {
			logger.LogAndSend(fmt.Sprintf("Uploader found %d unexpected files at the root", up.filesSeen))
		}

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

func (up *uploader) syncToAzure(fullPath string) error {
	if up == nil || up.azShareCl == nil {
		return initUploaderErr
	}

	// TODO: be smarter about when we actually need to do that. But just an optimization.
	_, err := up.azShareCl.Create(context.TODO(), nil)
	if err != nil && !fileerror.HasCode(err, fileerror.ShareAlreadyExists) {
		return fmt.Errorf("Azure share creation failed: %v", err)
	}

	parts := strings.Split(fullPath, string(filepath.Separator))
	lp := len(parts)
	if lp < 3 {
		return fmt.Errorf("unexpected path hierarchy, wanted 'someprefix/datetime/datatype/filename', got %s", fullPath)
	}

	datetime, datatype, filename := parts[lp-3], parts[lp-2], parts[lp-1]

	datetimeCl := up.azShareCl.NewDirectoryClient(datetime)
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

	tempFilename := filename + ".part"
	fClient := datatypeCl.NewFileClient(tempFilename)
	if _, err := fClient.Create(context.TODO(), fSize, nil); err != nil {
		return fmt.Errorf("failed to create file %v: %v", tempFilename, err)
	}

	if err := fClient.UploadFile(context.TODO(), f, nil); err != nil {
		return fmt.Errorf("failed to upload contents for file %v: %v", tempFilename, err)
	}

	// TODO: for Marlin, remove the force replace. We only enforce it now because
	// Will's has already uploaded a lot of partial files with the final name
	// (because they have been uploaded before the .part change). And so a rename
	// without overwriting would fail.
	repl := true
	rnPath := filepath.Join(datetime, datatype, filename)
	if _, err := fClient.Rename(context.TODO(), rnPath, &file.RenameOptions{ReplaceIfExists: &repl}); err != nil {
		return fmt.Errorf("failed to rename %v into %v: %v", tempFilename, filename, err)
	}

	logger.LogAndSend(fmt.Sprintf("Successfully uploaded to Azure: %v", fullPath))
	return nil
}

func (up *uploader) uploadAll() error {
	if up == nil || up.azShareCl == nil {
		return initUploaderErr
	}

	var stickyErr error
	setSticky := func(err error) {
		if stickyErr != nil {
			return
		}
		stickyErr = err
	}
	up.filesUploaded = 0
	up.filesToUpload = 0
	up.filesSeen = 0
	fn := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		name := d.Name()
		if !isUploadMarker(name) {
			up.filesSeen++
			return nil
		}

		fileToUpload := strings.TrimPrefix(strings.TrimSuffix(name, toUploadSuffix), toUploadPrefix)
		currentDir := filepath.Dir(p)
		fileToUploadPath := filepath.Join(currentDir, fileToUpload)
		if _, err := os.Stat(fileToUploadPath); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			err = fmt.Errorf("upload marker file (%s) found, but corresponding file (%s) not found", name, fileToUpload)
			logger.LogAndSend(err.Error())
			setSticky(err)
			return nil
		}
		up.filesToUpload++

		if err := up.syncToAzure(fileToUploadPath); err != nil {
			err = fmt.Errorf("while trying to upload %v: %v", fileToUploadPath, err)
			logger.LogAndSend(err.Error())
			setSticky(err)
			return nil
		}
		up.filesUploaded++

		if err := os.Remove(p); err != nil {
			err = fmt.Errorf("failed to remove upload marker %v: %v", p, err)
			logger.LogAndSend(err.Error())
			setSticky(err)
			return nil
		}

		return nil
	}

	if err := filepath.WalkDir(up.rootDir, fn); err != nil {
		setSticky(err)
	}
	return stickyErr
}

func (up *uploader) uploadRoot() error {
	if up == nil || up.azShareCl == nil {
		return initUploaderErr
	}

	files, err := os.ReadDir(up.rootDir)
	if err != nil {
		return err
	}

	up.filesSeen = 0
	for _, de := range files {
		if de.IsDir() {
			continue
		}

		name := de.Name()

		if !isSensorData(name) {
			up.filesSeen++
			continue
		}

		_, err := up.azShareCl.Create(context.TODO(), nil)
		if err != nil && !fileerror.HasCode(err, fileerror.ShareAlreadyExists) {
			return fmt.Errorf("Azure share creation failed: %v", err)
		}

		dirname, _ := datatype.DirType(datatype.SensorData)
		cl := up.azShareCl.NewDirectoryClient(dirname)
		if _, err := cl.Create(context.TODO(), nil); err != nil {
			if !fileerror.HasCode(err, fileerror.ResourceAlreadyExists) {
				return fmt.Errorf("failed to create sensordata directory: %v", err)
			}
		}

		fullPath := filepath.Join(up.rootDir, name)
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

		fClient := cl.NewFileClient(name)
		// TODO: since we never delete them from the drive, we're systematically going
		// to reupload them all at each run. Better safe than sorry, and probably not a big
		// deal for now.
		if _, err := fClient.Create(context.TODO(), fSize, nil); err != nil {
			return fmt.Errorf("failed to create file %v: %v", name, err)
		}

		if err := fClient.UploadFile(context.TODO(), f, nil); err != nil {
			return fmt.Errorf("failed to upload contents for file %v: %v", name, err)
		}

		logger.LogAndSend(fmt.Sprintf("Successfully uploaded to Azure: %v", fullPath))
	}
	return nil
}

func probeVid(fullPath string) error {
	// TODO: embed ffprobe?
	return nil
	out, err := exec.Command("ffprobe", "-hide_banner", fullPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v", string(out))
	}
	return nil
}
