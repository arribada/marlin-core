// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package datatype

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrianmo/go-nmea"
)

func init() {
	// we do this here so that it gets set both in core and whisper
	log.SetFlags(log.LstdFlags | log.LUTC)
}

const (
	Audio        = iota
	Video        = iota
	Speech       = iota
	GPS          = iota
	FishingEvent = iota
	ButtonPress  = iota
	SensorData   = iota
)

var (
	// we want a slice, for consistent looping order in populator.
	// also, we need the audio processing to happen before the stt, as
	// the stts will be created on the fly from the audio.
	// TODO: backport to main repo
	Dirnames = []string{
		"audio",
		"buttonpress",
		"gps",
		"stt",
		"video",
	}
	PrefixesByType = map[int]string{
		Audio:        "audio",
		Video:        "video",
		GPS:          "gps",
		Speech:       "stt",
		FishingEvent: "fishing",
		ButtonPress:  "buttonpress",
		SensorData:   "sensordata",
	}
	DirByType = map[int]string{
		Audio:        "audio",
		Video:        "video",
		GPS:          "gps",
		Speech:       "stt",
		FishingEvent: "fishing",
		ButtonPress:  "buttonpress",
		SensorData:   "sensordata",
	}
	ExtByType = map[int]string{
		Audio:        "wav",
		Video:        "mp4",
		GPS:          "json",
		Speech:       "json",
		FishingEvent: "json",
		ButtonPress:  "json",
		SensorData:   "csv",
	}
)

const TmpDirName = ".tmp"

func FilePrefix(dataType int) (string, error) {
	prefix, ok := PrefixesByType[dataType]
	if !ok {
		return "", fmt.Errorf("unsupported data type: %v", dataType)
	}
	return prefix, nil
}

func FileExt(dataType int) (string, error) {
	ext, ok := ExtByType[dataType]
	if !ok {
		return "", fmt.Errorf("unsupported data type: %v", dataType)
	}
	return ext, nil
}

func DirType(dataType int) (string, error) {
	dir, ok := DirByType[dataType]
	if !ok {
		return "", fmt.Errorf("unsupported data type: %v", dataType)
	}
	return dir, nil
}

func NewFileName(dataType int, t time.Time) (string, error) {
	prefix, err := FilePrefix(dataType)
	if err != nil {
		return "", err
	}
	ext, err := FileExt(dataType)
	if err != nil {
		return "", err
	}

	dayTime := t.UTC().Format(time.RFC3339)
	dayTime = strings.Replace(dayTime, ":", "_", -1)
	name := fmt.Sprintf("%s-%s.%s", prefix, dayTime, ext)

	return name, nil
}

func CreateFile(rootDir string, dataType int) (*os.File, error) {
	name, err := NewFileName(dataType, time.Now())
	if err != nil {
		return nil, err
	}
	return os.Create(filepath.Join(rootDir, name))
}

func WriteEventFile(dir string, e interface{}, dtype int) error {
	// write in a temp dir, because we want to finish with a rename in the fsnotify
	// watched dir.
	tmpDir := filepath.Join(dir, TmpDirName)
	if err := os.MkdirAll(tmpDir, os.ModePerm); err != nil {
		return err
	}
	f, err := CreateFile(tmpDir, dtype)
	if err != nil {
		return err
	}
	name := f.Name()

	enc := json.NewEncoder(f)
	// TODO: since we're encoding from untyped interface{}, if we ever need json tags,
	// they won't be taken into account. But otoh, the dynamic types we need are in
	// core/pkg/types which we can't/don't want to depend from atm.
	if err := enc.Encode(e); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	newName := filepath.Join(dir, filepath.Base(name))
	return os.Rename(name, newName)
}

func FileTime(dataType int, name string) (time.Time, error) {
	zero := time.Time{}
	// TODO: maybe allow dataType to be empty, and strip blindly.
	prefix, err := FilePrefix(dataType)
	if err != nil {
		return zero, err
	}
	if !strings.HasPrefix(name, prefix) {
		return zero, fmt.Errorf("mismatch for data type in name %s: expected %s", name, prefix)
	}

	name = strings.TrimPrefix(name, prefix+"-")

	ext, err := FileExt(dataType)
	if err != nil {
		return zero, err
	}
	if !strings.HasSuffix(name, ext) {
		return zero, fmt.Errorf("mismatch for extension in name %s: expected %s", name, ext)
	}

	name = strings.TrimSuffix(name, "."+ext)

	name = strings.Replace(name, "_", ":", -1)
	return time.Parse(time.RFC3339, name)
}

func ArchivedPath(dataType int, t time.Time) (string, string, error) {
	day := t.UTC().Format(time.DateOnly)

	prefix, err := FilePrefix(dataType)
	if err != nil {
		return "", "", err
	}
	ext, err := FileExt(dataType)
	if err != nil {
		return "", "", err
	}
	dirType, err := DirType(dataType)
	if err != nil {
		return "", "", err
	}

	dirName := filepath.Join(day, dirType)

	dayTime := t.UTC().Format(time.RFC3339)
	dayTime = strings.Replace(dayTime, ":", "_", -1)
	name := fmt.Sprintf("%s-%s.%s", prefix, dayTime, ext)

	return dirName, name, nil
}

func IsValidName(name string) (bool, time.Time, error) {
	var t time.Time
	parts := strings.SplitN(name, "-", 2)
	if len(parts) != 2 {
		return false, t, fmt.Errorf("couldn't parse prefix, invalid number of parts: %d", len(parts))
	}
	dt := parts[0]
	found := false
	for _, v := range PrefixesByType {
		if dt == v {
			found = true
			break
		}
	}
	if !found {
		return false, t, fmt.Errorf("invalid prefix in %v: %v", name, dt)
	}

	ext := filepath.Ext(name)
	found = false
	for _, v := range ExtByType {
		if ext == "."+v {
			found = true
			break
		}
	}
	if !found {
		return false, t, fmt.Errorf("invalid extension in %v: %v", name, ext)
	}

	date := strings.Replace(strings.TrimSuffix(strings.TrimPrefix(name, dt+"-"), ext), "_", ":", -1)
	var err error
	t, err = time.Parse(time.RFC3339, date)
	if err != nil {
		return false, t, fmt.Errorf("invalid date part in %v: %v", name, err)
	}

	return true, t.UTC(), nil
}

type Segment struct {
	Start time.Duration
	End   time.Duration
	Text  string
}

// for "short" RMC, since we want to omit some of the fields of the original
// nmea.RMC, and they don't have defined omitempty there.
// TODO: keep it in sync with nmea.RMC with go generate or something. Or maybe
// do something smarter directly from nmea.RMC, I dunno.
type SRMC struct {
	Raw       string
	Time      nmea.Time // Time Stamp
	Validity  string    // validity - A-ok, V-invalid
	Latitude  float64   // Latitude
	Longitude float64   // Longitude
	Speed     float64   // Speed in knots
	Course    float64   // True course
	Date      nmea.Date // Date
	Variation float64   // Magnetic variation
	FFAMode   string    // FAA mode indicator (filled in NMEA 2.3 and later)
	NavStatus string    // Nav Status (NMEA 4.1 and later)
}

type SRMCer interface {
	Inner() SRMC
}

func (rmc SRMC) Inner() SRMC {
	return rmc
}

type SRMCplus struct {
	HDOP float64
	SRMC SRMC
}

func (rmc SRMCplus) Inner() SRMC {
	return rmc.SRMC
}

func (rmc SRMC) ToGoTime() (time.Time, error) {
	dateString := fmt.Sprintf("20%02d-%02d-%02d", rmc.Date.YY, rmc.Date.MM, rmc.Date.DD)

	seconds := float64(rmc.Time.Second) + float64(rmc.Time.Millisecond)/1000
	timeString := fmt.Sprintf("%02d:%02d:%012.9f", rmc.Time.Hour, rmc.Time.Minute, seconds)

	s := fmt.Sprintf("%sT%sZ", dateString, timeString)
	return time.Parse(time.RFC3339Nano, s)
}
