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

	"github.com/arribada/marlin-common/pkg/datatype"
	"github.com/arribada/marlin-common/pkg/sat"
	lpmio "github.com/arribada/marlin-core/collector/pkg/lpm/io"
	"github.com/arribada/marlin-core/collector/pkg/types"
	"golang.org/x/sys/unix"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/directory"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/file"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/fileerror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/share"
	"github.com/fsnotify/fsnotify"
)

var logger = log.New(os.Stderr, "Watcher: ", log.Default().Flags()|log.Lmsgprefix)

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

// TODO: gross, but we know we only have one instance of a DuWatcher for now so...
// Just do better later. We might ask the databrowser or the GC instead anyway.
var (
	theDuWatcherMu sync.RWMutex
	theDuWatcher   *DuWatcher
)

func GetStorageUsage() float64 {
	theDuWatcherMu.RLock()
	defer theDuWatcherMu.RUnlock()
	if theDuWatcher == nil {
		return 0
	}
	return theDuWatcher.Used
}

type EventsWatcher struct {
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

func New(cfg Config, theChans *types.Chans) (*EventsWatcher, error) {
	if theChans == nil {
		return nil, errors.New("chans not provided")
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
	theDuWatcherMu.Lock()
	theDuWatcher = du
	theDuWatcherMu.Unlock()

	ew.chans = theChans
	if cfg.VideoAlwaysOn {
		ew.isVideoRecording = true
	}

	return ew, nil
}

func (ew *EventsWatcher) trackVideoRecState(ctx context.Context) {
	for {
		var isRecording bool
		select {
		case <-ctx.Done():
			return
		case isRecording = <-ew.chans.VideoState:
		}
		if ew.config.VideoAlwaysOn {
			// isVideoRecording already set to true in New.
			continue
		}
		ew.muVideoRec.Lock()
		ew.isVideoRecording = isRecording
		ew.muVideoRec.Unlock()
	}
}

/*
func (ew *EventsWatcher) videoRecState() bool {
	ew.muVideoRec.RLock()
	defer ew.muVideoRec.RUnlock()
	return ew.isVideoRecording
}
*/

func (ew *EventsWatcher) trackButtonPresses(ctx context.Context) {
	ew.lastButtonPress = time.Now().UTC()
	for {
		var bp bool
		select {
		case <-ctx.Done():
			return
		case bp = <-ew.chans.ButtonPress:
		}
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
				now := time.Now().UTC()
				if now.Sub(ew.lastButtonPress) > time.Second {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			now := time.Now().UTC()
			event := types.Event{
				ButtonPress: &types.ButtonPress{
					Time:  now,
					Start: bp,
				},
			}
			if err := datatype.WriteEventFile(ew.config.DataDir, event, datatype.ButtonPress); err != nil {
				logger.Printf("while writing button press event: %v", err)
			}
			ew.lastButtonPress = now
			if bp {
				var latestPos types.Position
				tm := time.NewTimer(5 * time.Second)
				select {
				case ew.chans.Position <- types.Position{}:
					select {
					case latestPos = <-ew.chans.Position:
					case <-tm.C:
						logger.Printf("timed out on receiving GPS position for alert. Proceeding without a position.")
					}
				case <-tm.C:
					logger.Printf("timed out on requesting GPS position for alert. Proceeding without a position.")
				}

				if err := lpmio.SendAlert(sat.Alert{
					Time:      now,
					Latitude:  latestPos.Latitude,
					Longitude: latestPos.Longitude,
				}); err != nil {
					logger.Printf("failed to send alert on button press: %v", err)
				}
			}
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

// TODO: move that to the GC? or to the data browser?
func (du *DuWatcher) loop(ctx context.Context) {
	if du == nil {
		logger.Printf("Storage space watcher not enabled")
		return
	}

	// Always log storage space once on startup
	if err := du.Update(); err != nil {
		logger.Printf("while storage watcher updating: %v", err)
	} else {
		logger.Printf("Storage space usage: %dMB/%dMB used", (du.Total-du.Free)/1e6, du.Total/1e6)
	}

	for {
		if err := du.Update(); err != nil {
			logger.Printf("while storage watcher updating: %v", err)
		}

		if du.Used > du.threshold {
			logger.Printf("WARNING: Storage space threshold (%4.2f) exceeded: %4.2f. Space left: %dMB", du.threshold, du.Used, du.Free/1e6)
		}

		if du.interval <= 0 {
			logger.Printf("Storage watcher loop disabled by interval, only trying once")
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
	defer sw.watcher.Close()

	go sw.trackVideoRecState(ctx)
	go sw.trackButtonPresses(ctx)
	go sw.up.loop(ctx)
	go sw.du.loop(ctx)

	logger.Printf("starting to watch %v", sw.config.DataDir)

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
			logger.Printf("file watcher error: %v", err)
		}
	}
}

func (ew *EventsWatcher) handleEvent(event fsnotify.Event) {
	name := path.Base(event.Name)
	// should actually be the same as event.Name though
	fullPath := filepath.Join(ew.config.DataDir, name)

	fi, err := os.Stat(fullPath)
	if err != nil {
		logger.Printf("error while statting file %s: %v", fullPath, err)
		return
	}

	if fi.Mode().IsDir() {
		return
	}

	if isSpeech(name) {
		logger.Printf("New speech file detected: %s", name)
		go func() {
			if _, err := ew.archiveFile(datatype.Speech, fullPath); err != nil {
				logger.Printf("error while archiving file %s: %v", fullPath, err)
				return
			}
		}()
		return
	}

	if isAudio(name) {
		logger.Printf("New audio file detected: %s", name)
		go func() {
			if _, err := ew.archiveFile(datatype.Audio, fullPath); err != nil {
				logger.Printf("error while archiving file %s: %v", fullPath, err)
				return
			}
		}()
		return
	}

	if isVideo(name) {
		logger.Printf("New video file detected: %s", name)
		go func() {
			newFullPath, err := ew.archiveFile(datatype.Video, fullPath)
			if err != nil {
				logger.Printf("error while archiving file %s: %v", fullPath, err)
				if newFullPath == "" {
					return
				}
				// we keep going because we still do have a vid file at newFullPath to probe
			}
			if _, err := ProbeVid(newFullPath); err != nil {
				logger.Printf("error while probing video file %s: %v", newFullPath, err)
				return
			}
		}()
		return
	}

	if isGPS(name) {
		if ew.config.Verbose {
			logger.Printf("New gps file detected: %s", name)
		}
		go func() {
			if _, err := ew.archiveFile(datatype.GPS, fullPath); err != nil {
				logger.Printf("error while archiving file %s: %v", fullPath, err)
				return
			}
		}()
		return
	}

	if isButtonPress(name) {
		logger.Printf("New button press event file detected: %s", name)
		go func() {
			if _, err := ew.archiveFile(datatype.ButtonPress, fullPath); err != nil {
				logger.Printf("error while archiving file %s: %v", fullPath, err)
				return
			}
		}()
		return
	}

	logger.Printf("Unsupported file (%v), doing nothing", name)
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

	// TODO: if the program dies right here (e.g. because the vessel
	// power shuts down), our file will never get uploaded, since it's
	// never been marked. Fix that.
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

var (
	connectionStatusMu sync.RWMutex
	// basically loosely reflects whether the latest upload was successful
	connectionStatus bool
)

func GetConnectionStatus() bool {
	connectionStatusMu.RLock()
	defer connectionStatusMu.RUnlock()
	return connectionStatus
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
		logger.Printf("Uploading to Azure not enabled")
		return
	}

	for {
		run := &types.UploadRun{
			TimeStart: time.Now().UTC(),
		}
		if err := up.uploadAll(); err != nil {
			logger.Printf("while the uploader was walking the tree: %v", err)
			run.Err = err
		}
		run.TimeEnd = time.Now().UTC()
		run.FilesUploaded = up.filesUploaded
		run.FilesSeen = up.filesSeen
		run.FilesToUpload = up.filesToUpload
		// TODO: better report
		logger.Printf("upload run report: %#v", run)

		connectionStatusMu.Lock()
		connectionStatus = (run.Err == nil)
		connectionStatusMu.Unlock()

		// TODO: re-add something to monitor there's no files at the root?

		if up.interval <= 0 {
			logger.Printf("Uploading loop disabled by interval, only trying once")
			return
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		if run.Err != nil {
			// TODO: add a bit of a backoff, especially on systematic error?
			// the goal is of course to find and phase out all such errors though.
			logger.Printf("restarting upload run immediately because there was an error")
			time.Sleep(3 * time.Second)
			continue
		}

		tm := time.NewTimer(up.interval)
		select {
		case <-ctx.Done():
			return
		case <-tm.C:
		}
	}
}

// uploadAll (lexicographically) goes through the whole tree of
// data, and for each upload marker it finds, it uploads the corresponding
// data file. It removes the upload marker right after the data file
// is uploaded. So it is a bit wastefull to go through the whole tree
// everytime, but relying on the upload markers presence allows us to
// go through it all pretty fast nonetheless. Also, we rely on the
// garbage collector to remove older uploaded data as we go, so the
// size of the tree is always limited.
// This simple approach allows us to act very simply as well on the
// database pipeline side: on Azure, any (day) directory that is not
// the last one chronologically is by design complete, and therefore
// can and should be considered and downloaded only once. So we only
// have to systematically redo the last day on each loop, just in case
// it is not yet complete. And even then, we have checks in place to
// avoid processing files (of the last day) that we have already done.
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
			// TODO: think whether to investigate that case any further.
			logger.Printf("upload marker file (%s) found, but corresponding file (%s) not found. Skipping.", name, fileToUpload)
			continue
		}
		up.filesToUpload++

		done, err := up.uploadFile(datatypeCl, typePath, fileToUpload)
		if err != nil {
			return fmt.Errorf("while trying to upload %v: %v", fileToUploadPath, err)
		}
		// because we didn't upload it if it was an empty file
		if !done {
			// Keep the upload marker around as a proof for later investigation
			logger.Printf("upload marker file (%s) found, but corresponding file (%s) is empty. Skipping.", name, fileToUpload)
			continue
		}
		up.filesUploaded++

		markerPath := filepath.Join(typePath, name)
		if err := os.Remove(markerPath); err != nil {
			return fmt.Errorf("failed to remove upload marker %v: %v", markerPath, err)
		}
	}

	return nil
}

func (up *uploader) uploadFile(datatypeCl *directory.Client, typeDir, filename string) (bool, error) {
	fullPath := filepath.Join(typeDir, filename)
	f, err := os.OpenFile(fullPath, os.O_RDONLY, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	fInfo, err := f.Stat()
	if err != nil {
		return false, err
	}
	fSize := fInfo.Size()
	// trying to upload an empty file will result in an Azure error, so we purposefully ignore it.
	// TODO: could there ever be a case that this file would
	// eventually not be empty and we incorrectly skipped it?
	if fSize == 0 {
		return false, nil
	}

	// TODO: should we validate more strictly, i.e. by common datatype?
	if strings.HasSuffix(filename, ".") {
		logger.Printf("skipping upload of empty file %v", filename)
		// skip hidden files, notably the .DS_Store bs
		return false, nil
	}

	parts := strings.Split(typeDir, "/")
	nb := len(parts)
	if nb < 3 {
		return false, fmt.Errorf("unexpected number of parts in type dir: %d", len(parts))
	}
	dayDir, tpDir := parts[nb-2], parts[nb-1]
	tempFilename := filename + ".part"
	fClient := datatypeCl.NewFileClient(tempFilename)
	if _, err := fClient.Create(context.TODO(), fSize, nil); err != nil {
		return false, fmt.Errorf("failed to create file %v: %v", tempFilename, err)
	}

	if err := fClient.UploadFile(context.TODO(), f, nil); err != nil {
		return false, fmt.Errorf("failed to upload contents for file %v: %v", tempFilename, err)
	}

	rnPath := filepath.Join(dayDir, tpDir, filename)
	if _, err := fClient.Rename(context.TODO(), rnPath, nil); err != nil {
		if !fileerror.HasCode(err, fileerror.ResourceAlreadyExists) {
			return false, fmt.Errorf("failed to rename %v into %v: %v", tempFilename, filename, err)
		}

		// remote file already exists, investigate further.
		rClient := datatypeCl.NewFileClient(filename)
		resp, err := rClient.GetProperties(context.TODO(), nil)
		if err != nil {
			return false, fmt.Errorf("could not get properties of %v: %v", filename, err)
		}
		rSize := *(resp.ContentLength)
		if rSize > fSize {
			return false, fmt.Errorf("refusing to overwrite remote file (%v) with smaller local one", filename)
		}
		logger.Printf("Overwriting (while renaming) remote file %v with local file", filename)
		b := true
		if _, err := fClient.Rename(context.TODO(), rnPath,
			&file.RenameOptions{ReplaceIfExists: &b}); err != nil {
			return false, fmt.Errorf("failed to rename %v into %v: %v", tempFilename, filename, err)
		}
	}

	logger.Printf("Successfully uploaded to Azure: %v", fullPath)
	return true, nil
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
