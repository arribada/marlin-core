// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type reporter struct {
	dataDir string
}

func getPreviousFishingDay(dataDir string) (string, error) {
	dir, err := os.ReadDir(dataDir)
	if err != nil {
		return "", err
	}
	today := time.Now()
	todayAsDay := today.Format(time.DateOnly)
	// since they're already sortedy by name, and the one we want is close to the end, let's go backwards
	for i := len(dir) - 1; i > -1; i-- {
		name := dir[i].Name()
		if !dir[i].IsDir() {
			continue
		}
		// might be on disk historically because i360/CEFAS
		if name == "sensordata" {
			continue
		}
		if name == ".tmp" {
			continue
		}
		day, err := time.Parse(time.DateOnly, name)
		if err != nil {
			return "", err
		}
		if name == todayAsDay {
			// we don't want to pick today
			continue
		}
		if day.Before(today) {
			// found it
			return day.Format(time.DateOnly), nil
		}
	}
	return "", errors.New("not found")
}

// TODO: return JSON object?
func doDay(dataDir string, day string, dt []string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", day)
	for _, v := range dt {
		names, err := listData(dataDir, day, v)
		if err != nil {
			if err == noDataErr {
				fmt.Fprintf(&b, "no %v data\n", v)
				continue
			}
			return "", err
		}

		tt, err := startEnd(names)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s time window: %s %s\n", v, tt[0].Format(time.TimeOnly), tt[1].Format(time.TimeOnly))

		inter, err := learnInterval(names)
		if err != nil {
			return "", err
		}

		holes, err := findHoles(inter, names)
		if err != nil {
			return "", err
		}
		if len(holes) > 0 {
			b.WriteString("Missing data:\n")
			for _, v := range holes {
				fmt.Fprintf(&b, "%s %s\n", v.start.Format(time.TimeOnly), v.end.Format(time.TimeOnly))
			}
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

var noDataErr error = errors.New("no data of that type")

func listData(dataDir, day, dt string) ([]string, error) {
	dirPath := filepath.Join(dataDir, day, dt)
	dir, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, noDataErr
		}
		return nil, err
	}
	var names []string
	for _, v := range dir {
		names = append(names, v.Name())
	}
	return names, err
}

func startEnd(names []string) ([2]time.Time, error) {
	var ret [2]time.Time
	start, err := timePart(names[0])
	if err != nil {
		return ret, err
	}
	end, err := timePart(names[len(names)-1])
	if err != nil {
		return ret, err
	}
	return [2]time.Time{start, end}, nil
}

func timePart(filename string) (time.Time, error) {
	var t time.Time
	parts := strings.Split(filename, "T")
	if len(parts) != 2 {
		return t, fmt.Errorf("unexpected number of parts: %v", filename)
	}
	tp := parts[1][:8]
	tp = strings.Replace(tp, "_", ":", 3)
	t, err := time.Parse(time.TimeOnly, tp)
	if err != nil {
		return t, err
	}
	return t, nil
}

type hole struct {
	start time.Time
	end   time.Time
}

func findHoles(interval int64, names []string) ([]hole, error) {
	// arbitrarily define a 10% error margin for the interval
	var errorMargin = time.Duration(interval) * time.Second / 10
	var prevDate time.Time
	var holes []hole
	for k, v := range names {
		t, err := timePart(v)
		if err != nil {
			return nil, err
		}
		if k == 0 {
			prevDate = t
			continue
		}
		plannedDate := prevDate.Add(time.Duration(interval) * time.Second)
		minPlanned := plannedDate.Add(-errorMargin)
		maxPlanned := plannedDate.Add(errorMargin)
		if t.Before(minPlanned) || t.After(maxPlanned) {
			holes = append(holes, hole{start: prevDate, end: t})
		}
		prevDate = t
	}
	return holes, nil
}

func learnInterval(names []string) (int64, error) {
	var prevDate time.Time
	dist := make(map[int64]int)
	for k, v := range names {
		t, err := timePart(v)
		if err != nil {
			return 0, err
		}
		if k == 0 {
			prevDate = t
			continue
		}
		diff := t.Sub(prevDate)
		prevDate = t
		intdiff := int64(diff / time.Second)
		n, ok := dist[intdiff]
		if !ok {
			dist[intdiff] = 1
			continue
		}
		dist[intdiff] = n + 1
	}

	max := 0
	var inter int64
	for k, v := range dist {
		if v > max {
			max = v
			inter = k
		}
	}

	return inter, nil
}
