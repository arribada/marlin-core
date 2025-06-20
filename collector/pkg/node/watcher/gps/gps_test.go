// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package gps

import (
	"flag"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianmo/go-nmea"
	"github.com/arribada/marlin-core/collector/pkg/logger"
	"github.com/arribada/marlin-core/collector/pkg/node/gpsr"
	"github.com/arribada/marlin-core/collector/pkg/tests"

	"go-hep.org/x/hep/hplot"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

func TestActivity(t *testing.T) {
	l := logger.NewLogger()
	cfg := DefaultConfig
	cfg.DataDir = t.TempDir()
	g, err := NewGPSEvents(l, cfg)
	if err != nil {
		t.Fatal(err)
	}

	testdata := []gpsr.SRMCplus{
		{
			SRMC: gpsr.SRMC{
				Time: nmea.Time{
					Hour:        15,
					Minute:      19,
					Second:      19,
					Millisecond: 1234,
				},
				Speed: 0.473,
				Date: nmea.Date{
					DD: 28,
					MM: 06,
					YY: 24,
				},
			},
		},
	}

	if _, _, _, err := addEvents(g, testdata, false); err != nil {
		t.Fatal(err)
	}

	if g.fishingIndex != minLikelihood {
		t.Fatalf("unexpected initial fishing index, wanted %f, got %f", minLikelihood, g.fishingIndex)
	}

	testdata = tests.SRMCp

	indices, _, _, err := addEvents(g, testdata, false)
	if err != nil {
		t.Fatal(err)
	}

	topLikelihood := float64(0)
	topLikelihoodPos := 0
	for k, v := range indices {
		if v > topLikelihood {
			topLikelihood = v
			topLikelihoodPos = k
		}
	}

	// we expect to have detected a stop at the third event, and hence that shoud be where our max value is too
	if topLikelihoodPos != 2 {
		t.Fatalf("unexpected max likelihood position, wanted %d, got %d", 2, topLikelihoodPos)
	}
	if topLikelihood != maxLikelihood {
		t.Fatalf("unexpected max likelihood, wanted %f, got %f", maxLikelihood, topLikelihood)
	}

	// test that in the advance of new stops, the index decreases by itself, at the predicted rate
	// TODO: do the predicted rate part
	prevLikelihood := indices[2]
	for _, v := range indices[3:] {
		if v >= prevLikelihood {
			t.Fatalf("unexpected likelihood, it should be lower than previous one (%f), but got: %f", prevLikelihood, v)
		}
		prevLikelihood = v
	}

	// test that we have started an activity when we stopped, and that we recorded
	// an activity after we started moving again. The exactitude of the "after" is
	// subject to change depending on how our algo evolves.
	if g.lastActivity == nil {
		t.Fatal("no activity detected")
	}
	if g.lastActivity.SpeedStart != testdata[2].SRMC.Speed {
		t.Fatal("activity did not start when expected")
	}
	if g.lastActivity.SpeedEnd != testdata[4].SRMC.Speed {
		t.Fatal("activity did not end when expected")
	}
}

var (
	minS int
	maxS int
)

func init() {
	flag.IntVar(&minS, "minx", 0, "minX")
	flag.IntVar(&maxS, "maxx", math.MaxInt, "maxX")
}

// GPS file format before the addition of HDOP.
func TestOldRealData1a31d90599(t *testing.T) {
	testRealData(t, true)
}

func TestRealData(t *testing.T) {
	testRealData(t, false)
}

func testRealData(t *testing.T, old bool) {
	flag.Parse()
	l := logger.NewLogger()
	cfg := DefaultConfig
	cfg.DataDir = t.TempDir()
	cfg.MovingThreshold = 0.5
	g, err := NewGPSEvents(l, cfg)
	if err != nil {
		t.Fatal(err)
	}

	files, err := os.ReadDir("./testdata")
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}

	if len(files) == 0 {
		return
	}

	var x []float64
	var y []float64
	var z []float64
	count := 0
	minX := time.Duration(minS) * time.Second
	maxX := time.Duration(maxS) * time.Second
	if maxX < 0 {
		maxX = math.MaxInt64
	}
	startDone := false
	var start time.Time
	for _, v := range files {
		name := v.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if !strings.HasPrefix(name, "gps-") {
			continue
		}
		indices, speed, tees, err := g.AddEventsFileOpts(filepath.Join("./testdata", name), old, true)
		if err != nil {
			if strings.HasPrefix(err.Error(), `invalid character '\x00' looking for beginning of value`) {
				t.Logf("while adding events from %v: %v", name, err)
				continue
			}
			t.Fatalf("while adding events from %v: %v", name, err)
		}
		for k, v := range indices {
			now := tees[k]
			var s time.Duration
			if !startDone {
				start = now
				s = 0
				startDone = true
			} else {
				s = now.Sub(start)
			}
			if s < minX {
				continue
			}
			if s > maxX {
				continue
			}
			x = append(x, float64(s))
			y = append(y, v)
			z = append(z, speed[k])
			count++
		}
		//		z = append(z, speed...)
		// TODO: since we're treating all the files very fast, the resulting fishing
		// activity event files created will overwrite each other if we don't pause in
		// between. See later if we care about that.
		// time.Sleep(time.Second)
	}

	var tf plot.TickerFunc = func(min, max float64) []plot.Tick {
		interval := (max - min) / 40
		var ticks []plot.Tick
		next := min
		major := true
		for _, v := range x {
			if v < min {
				continue
			}
			if v > max {
				continue
			}
			if v < next {
				continue
			}
			t := plot.Tick{
				Value: v,
			}
			if major {
				t.Label = "blabla"
				major = false
			} else {
				major = true
			}
			ticks = append(ticks, t)
			next = v + interval
		}
		return ticks
	}

	p1 := plot.New()
	p1.Title.Text = "fishing activity"
	p1.X.Label.Text = "time"
	p1.X.Tick.Marker = plot.TimeTicks{
		Ticker: tf,
		Format: time.TimeOnly,
		Time: func(t float64) time.Time {
			return start.Add(time.Duration(t))
		},
	}

	p1.Y.Label.Text = "fishing activity index"

	if err = plotutil.AddScatters(p1, "", hplot.ZipXY(x, y)); err != nil {
		t.Fatalf("could not create scatters: %+v", err)
	}

	err = p1.Save(40*vg.Centimeter, 10*vg.Centimeter, "./fishing.png")
	if err != nil {
		t.Fatalf("could not save scatter plot: %+v", err)
	}

	p2 := plot.New()
	p2.Title.Text = "Boat Speed"
	p2.X.Label.Text = "time"
	p2.X.Tick.Marker = plot.TimeTicks{
		Ticker: tf,
		Format: time.TimeOnly,
		Time: func(t float64) time.Time {
			return start.Add(time.Duration(t))
		},
	}
	p2.Y.Label.Text = "Speed in knots"

	if err = plotutil.AddScatters(p2, "", hplot.ZipXY(x, z)); err != nil {
		t.Fatalf("could not create scatters: %+v", err)
	}

	err = p2.Save(40*vg.Centimeter, 10*vg.Centimeter, "./speed.png")
	if err != nil {
		t.Fatalf("could not save scatter plot: %+v", err)
	}
}
