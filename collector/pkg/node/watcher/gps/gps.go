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
	"github.com/arribada/marlin-core/common/pkg/datatype"

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

// TODO: change signature. directly returning indices and speed for now to
// facilitate experimenting.
func (g *GPSEvents) AddEventsFile(filename string) ([]float64, error) {
	idx, _, _, err := g.AddEventsFileOpts(filename, false, false)
	return idx, err
}

// TODO: remove
// old for backwards incompatible file format change from 1a31d90599.
func (g *GPSEvents) AddEventsFileOpts(filename string, old bool, forTests bool) ([]float64, []float64, []time.Time, error) {
	if g == nil {
		return nil, nil, nil, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	f, err := os.Open(filename)
	if err != nil {
		return nil, nil, nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)

	if old {
		var rmc []gpsr.SRMC
		if err := dec.Decode(&rmc); err != nil {
			return nil, nil, nil, err
		}
		return addEvents(g, rmc, forTests)
	}
	var rmc []gpsr.SRMCplus
	if err := dec.Decode(&rmc); err != nil {
		return nil, nil, nil, err
	}
	return addEvents(g, rmc, forTests)
}

func addEvents[obj gpsr.SRMCer](g *GPSEvents, objs []obj, forTests bool) ([]float64, []float64, []time.Time, error) {
	var fishingIndices []float64
	var speed []float64
	var tees []time.Time
	for _, v := range objs {
		rmc := v.Inner()
		if forTests {
			speed = append(speed, rmc.Speed)
		}
		t, err := g.handleEvent(rmc)
		if forTests {
			tees = append(tees, t)
		}
		if err != nil {
			_ = level.Error(g.klogger).Log("err", fmt.Sprintf("while handling a GPS event: %v", err))
			fishingIndices = append(fishingIndices, InvalidFishingIndex)
			continue
		}
		fishingIndices = append(fishingIndices, g.fishingIndex)
	}
	return fishingIndices, speed, tees, nil
}

func (g *GPSEvents) handleEvent(rmc gpsr.SRMC) (time.Time, error) {
	now, err := rmc.ToGoTime()
	if err != nil {
		return time.Time{}, err
	}
	defer func() {
		// TODO: only do it if no error at all?
		g.lastTime = now
		g.lastSpeed = rmc.Speed
	}()
	if rmc.Speed > g.cfg.MovingThreshold {
		g.hasStarted = true
	}
	if g.isStopped(rmc.Speed) {
		if g.hasStarted {
			g.fishingIndex = maxLikelihood
			g.lastStop = now
		} else {
			// we actually have never been on the move yet, so can't really count that stop
			// as fishing for now.
			g.fishingIndex = minLikelihood
		}

		if err := g.assessActivity(now, rmc.Speed); err != nil {
			return time.Time{}, fmt.Errorf("while assessing fishing activity: %v", err)
		}

		return now, nil
	}

	g.updateFromDeceleration(now, rmc.Speed)

	g.updateFromLastActivity(now)

	if err := g.assessActivity(now, rmc.Speed); err != nil {
		return time.Time{}, fmt.Errorf("while assessing fishing activity: %v", err)
	}

	return now, nil
}

func (g *GPSEvents) isStopped(currentSpeed float64) bool {
	return currentSpeed < g.cfg.MovingThreshold
}

func (g *GPSEvents) updateFromLastActivity(now time.Time) {
	if g.lastStop.IsZero() {
		g.fishingIndex = minLikelihood
		return
	}

	sinceLastStop := now.Sub(g.lastStop)
	sinceInSeconds := float64(sinceLastStop) / float64(time.Second)
	decrease := sinceInSeconds * g.relaxationRate

	g.fishingIndex -= decrease
	if g.fishingIndex <= 0 {
		g.fishingIndex = minLikelihood
	}
}

func (g *GPSEvents) updateFromDeceleration(now time.Time, speed float64) {
	if g.lastStop.IsZero() {
		g.fishingIndex = minLikelihood
		return
	}

	if g.lastTime.IsZero() {
		// no previous event recorded, cannot conclude anything
		return
	}
	deltaT := now.Sub(g.lastTime)
	if deltaT > time.Duration(g.cfg.MinGranularity)*time.Second {
		// unrelated measurements, cannot contribute anything
		return
	}
	deltaV := speed - g.lastSpeed
	if deltaV > 0 {
		// we're accelerating, cannot contribute anything
		return
	}
	decel := -deltaV / float64(deltaT/time.Second)
	g.fishingIndex = decel * g.cfg.DecelerationFactor
	if g.fishingIndex > maxLikelihood {
		g.fishingIndex = maxLikelihood
	}
}

func (g *GPSEvents) assessActivity(now time.Time, speed float64) error {
	if g.lastActivityStart.IsZero() {
		// we were not fishing so far
		if g.fishingIndex >= g.cfg.FishingActivityThreshold {
			_ = level.Debug(g.klogger).Log("msg", fmt.Sprintf("new fishing activity at %v", now.String()))
			// but we are now fishing
			g.lastActivityStart = now
			g.speedAtStart = speed
			g.indexAtStart = g.fishingIndex
			return nil
		}
		// we are still not fishing
		return nil
	}

	// we were fishing so far
	if g.fishingIndex >= g.cfg.FishingActivityThreshold {
		// and we are still fishing
		return nil
	}

	// we have stopped fishing.
	// backup values.
	start := g.lastActivityStart
	startSpeed := g.speedAtStart
	startIndex := g.indexAtStart

	// reset to zero value, as sentinel for next run.
	g.lastActivityStart = time.Time{}
	g.speedAtStart = -1
	g.indexAtStart = -1

	event := types.Event{
		FishingActivity: &types.FishingActivity{
			TimeStart:  start,
			TimeEnd:    now,
			SpeedStart: startSpeed,
			SpeedEnd:   speed,
			IndexStart: startIndex,
			IndexEnd:   g.fishingIndex,
		},
	}
	g.lastActivity = event.FishingActivity

	// TODO: maybe move the actual writing out of here, and make assessActivity
	// return the activity. API still in flux anyway.
	_ = level.Debug(g.klogger).Log("msg", fmt.Sprintf("end of fishing activity at %v", now.String()))
	if err := datatype.WriteEventFile(g.cfg.DataDir, event, datatype.FishingEvent); err != nil {
		return fmt.Errorf("while writing fishing activity event: %v", err)
	}

	return nil
}
