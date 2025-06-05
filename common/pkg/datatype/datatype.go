// Copyright (c) The Arribada initiative.
// Licensed under the MIT License.

package datatype

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
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
