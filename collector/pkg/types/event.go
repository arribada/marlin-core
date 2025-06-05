// Copyright (c) The Arribada initiative.
// Licensed under the MIT License.

// TODO: maybe later move that whole file in a new pkg/watcher/event package

package types

import (
	"time"
)

const (
	UnknownEvent = iota
	CoreStartupEvent
	FishingEvent
	StorageAlertEvent
	// future events might include Bycatch, Sighting, Alert...
	UploadRunEvent
	LoggingEvent
	ButtonPressEvent
)

type FishingActivity struct {
	// TimeStart is when fishing activity started.
	TimeStart time.Time
	// TimeEnd is when fishing activity ended.
	TimeEnd time.Time
	// SpeedStart is the velocity (in knots) when activity started.
	SpeedStart float64
	// SpeedEnd is the velocity (in knots) when activity ended.
	SpeedEnd float64
	// IndexStart is the fishing activity index when activity started.
	IndexStart float64
	// EndIndex is the fishing activity index when activity ended.
	IndexEnd float64
}

type CoreStartup struct {
	TimeStart time.Time
}

type StorageAlert struct {
	Time       time.Time
	TotalSpace uint64
	FreeSpace  uint64
}

type UploadRun struct {
	TimeStart     time.Time
	TimeEnd       time.Time
	Err           error
	FilesUploaded int
	FilesSeen     uint64
	FilesToUpload int
}

type Logging struct {
	Time time.Time
	Msg  string
}

// TODO: maybe later have a UserInput event wrapping that one.
type ButtonPress struct {
	Time  time.Time
	Start bool
}

func (bp ButtonPress) String() string {
	if bp.Start {
		return "start"
	}
	return "stop"
}

type Event struct {
	FishingActivity *FishingActivity `json:",omitempty"`
	CoreStartup     *CoreStartup     `json:",omitempty"`
	StorageAlert    *StorageAlert    `json:",omitempty"`
	UploadRun       *UploadRun       `json:",omitempty"`
	Logging         *Logging         `json:",omitempty"`
	ButtonPress     *ButtonPress     `json:",omitempty"`
}

func (e *Event) Kind() int {
	switch {
	case e.FishingActivity != nil:
		return FishingEvent
	case e.CoreStartup != nil:
		return CoreStartupEvent
	case e.StorageAlert != nil:
		return StorageAlertEvent
	case e.UploadRun != nil:
		return UploadRunEvent
	case e.Logging != nil:
		return LoggingEvent
	case e.ButtonPress != nil:
		return ButtonPressEvent
	}
	return UnknownEvent
}

// Chans is the set of various channels for the core components to
// communicate with each other.
type Chans struct {
	// VideoCtrl is for the button presses handler to instruct the video recorder to
	// start/stop.
	VideoCtrl chan bool
	// VideoState is for the button presses handler to inform the data watcher
	// whether video recording is ongoing.
	VideoState chan bool
	// ButtonPress is for the button presses handler to instruct the data watcher to
	// create a button press event file.
	ButtonPress chan bool
}
