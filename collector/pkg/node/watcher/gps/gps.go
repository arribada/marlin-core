// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package gps

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/arribada/marlin-core/collector/pkg/logger"
	"github.com/arribada/marlin-core/collector/pkg/node/gpsr"
	"github.com/arribada/marlin-core/collector/pkg/types"

	klog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
)

const ComponentName = "gps data watcher"

type Config struct {
	// DataDir is the location where we create fishing activity events.
	DataDir string

	// MovingThreshold is the speed (in knots) below which we are considered
	// immobile.
	MovingThreshold float64
	// FishingActivityThreshold is the value for the fishing activity index above
	// which a fishing activity event is created.
	FishingActivityThreshold float64
	// the maximum period (in seconds) in between 2 GPS measurements to consider them related.
	MinGranularity int
	// the period it takes, if no fishing activity happens in the meantime, to go
	// back down from peak activity (index at max value == 1), to no activity at all
	// (index at min value == 0). In seconds.
	RelaxationPeriod int
	// How much the boat's deceleration is taken into account to compute the fishing activity index.
	DecelerationFactor float64
}

var DefaultConfig = Config{
	MovingThreshold:          0.5,
	FishingActivityThreshold: 0.8,
	MinGranularity:           300,
	RelaxationPeriod:         6 * 3600,
	DecelerationFactor:       0.25,
}

const (
	// arbitrary.
	maxLikelihood = 0.99
	minLikelihood = 0.01
	// sentinel for when something went wrong for this event.
	InvalidFishingIndex = -1.0
)

func NewGPSEvents(lgr klog.Logger, cfg Config) (*GPSEvents, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("DataDir not specified")
	}

	lgr, err := logger.ApplyFilter("info", lgr)
	if err != nil {
		return nil, err
	}

	return &GPSEvents{
		klogger:        klog.With(lgr, "component", ComponentName),
		cfg:            cfg,
		relaxationRate: float64(1) / float64(cfg.RelaxationPeriod),
	}, nil
}

type GPSEvents struct {
	klogger klog.Logger
	cfg     Config

	// basically locks everything
	mu sync.Mutex

	// [0-1], with 0 not fishing, 1 is certainty of fishing
	fishingIndex float64

	// have we ever been at least once above cfg.MovingThreshold
	hasStarted bool
	// last time we noted ourselves as being stopped
	lastStop time.Time

	// speed from previous event
	lastSpeed float64
	// timestamp from previous event
	lastTime time.Time

	// the period it takes, if no fishing activity happens in the meantime, to go
	// back down from peak activity (index at max value == 1), to no activity at all
	// (index at min value == 0). in seconds.
	// relaxation rate is the inverse of that period.
	relaxationRate float64

	// the moment when the current fishing activity started.
	// zero time if no currently on-going activity.
	lastActivityStart time.Time
	// the boat velocity when we started fishing.
	speedAtStart float64
	// the fishing index when we started fishing.
	indexAtStart float64
	// last fishing activity that was detected and written out. mainly to help with
	// testing.
	lastActivity *types.FishingActivity
}

func (g *GPSEvents) AddEventsFile(filename string) ([]float64, []time.Time, error) {
	if g == nil {
		return nil, nil, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	f, err := os.Open(filename)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)

	var rmc []gpsr.SRMCplus
	if err := dec.Decode(&rmc); err != nil {
		return nil, nil, err
	}
	return addEvents(g, rmc)
}

func addEvents[obj gpsr.SRMCer](g *GPSEvents, objs []obj) ([]float64, []time.Time, error) {
	var speed []float64
	var tees []time.Time
	for _, v := range objs {
		rmc := v.Inner()
		speed = append(speed, rmc.Speed)
		t, err := rmc.ToGoTime()
		if err != nil {
			return nil, nil, err
		}
		tees = append(tees, t)
		if err != nil {
			_ = level.Error(g.klogger).Log("err", fmt.Sprintf("while handling a GPS event: %v", err))
			continue
		}
	}
	return speed, tees, nil
}
