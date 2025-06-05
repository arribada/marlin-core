package main

import (
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"syscall"
	"time"
)

const (
	gpsDevice = "/tmp/gps"
)

var (
	timeStep = 5 // in seconds
	// TODO: random init
	initLat      = 4524.70404 / 100
	initLong     = 310.90541 / 100
	initSpeed    = 5.0
	maxStepSpeed = 1 // knots/s
)

func main() {
	if err := os.RemoveAll(gpsDevice); err != nil {
		log.Fatal(err)
	}
	if err := syscall.Mkfifo(gpsDevice, 0666); err != nil {
		log.Fatal(err)
	}
	f, err := os.OpenFile(gpsDevice, os.O_RDWR, os.ModeNamedPipe)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	curLat := initLat
	curLong := initLong
	curSpeed := initSpeed
	curTime := time.Now()
	// TODO: make them dynamic.
	stepLat := 0.0001
	stepLong := 0.0001

	// $GPRMC,104813.00,A,4524.70404,N,00310.90541,E,0.022,,310325,,,A*72
	// $GPRMC,220516,A,5133.82,N,00042.24,W,173.8,231.8,130694,004.2,W*70

	for {
		data := fmt.Sprintf("$GPRMC,%s,A,%010.5f,N,%011.5f,W,%05.3f,231.8,130694,004.2,W*70\n",
			curTime.Format("150405"),
			curLat*100,
			curLong*100,
			curSpeed)
		if _, err := f.Write([]byte(data)); err != nil {
			log.Fatal(err)
		}

		var nextLat, nextLong, nextSpeed float64
		// TODO: make lat, long derived from speed (or vice-versa), but maybe keep pretending Euclid was right.
		stepSign := rand.Float64() - 0.5
		if stepSign < 0 {
			nextLat = curLat - stepLat
		} else {
			nextLat = curLat + stepLat
		}
		if nextLat > 90 {
			nextLat -= 180
		}
		if nextLat < -90 {
			nextLat += 180
		}

		stepSign = rand.Float64() - 0.5
		if stepSign < 0 {
			nextLong = curLong - stepLong
		} else {
			nextLong = curLong + stepLong
		}
		if nextLong > 180 {
			nextLong -= 360
		}
		if nextLong < -180 {
			nextLong += 360
		}

		// TODO: more realistic speed changes
		// And more importantly, simulate stops.
		stepSpeed := rand.Float64()
		for i := 0; i < timeStep; i++ {
			stepSign = rand.Float64() - 0.5
			if stepSign < 0 {
				nextSpeed = curSpeed - stepSpeed
			} else {
				nextSpeed = curSpeed + stepSpeed
			}
			if nextSpeed < 0 {
				nextSpeed = 0
			}
		}

		nextTime := curTime.Add(time.Duration(timeStep) * time.Second)

		curLat = nextLat
		curLong = nextLong
		curSpeed = nextSpeed
		curTime = nextTime

		time.Sleep(time.Duration(timeStep) * time.Second)
	}
}
