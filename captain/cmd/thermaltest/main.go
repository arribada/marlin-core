// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

const FFmpgebin = "/usr/local/bin/ffmpeg"

var inputts = "/perm/input.ts"

func main() {

	monitorTemperature(context.Background())

	serial()
}

func concurrent() {
	i := 0
	for {
		time.Sleep(10 * time.Second)
		go func(n int) {
			_, _, err := tsToMP4(inputts, n)
			if err != nil {
				log.Fatal(err)
			}
		}(i)
		i++
	}
}

func serial() {
	i := 0
	for {
		_, _, err := tsToMP4(inputts, i)
		if err != nil {
			log.Fatal(err)
		}
		i++
	}
}

func tsToMP4(input string, n int) (string, string, error) {
	output := strings.Replace(input, ".ts", fmt.Sprintf("%d.mp4", n), 1)
	var stderr bytes.Buffer
	var stdout bytes.Buffer
	cmd := exec.Command(FFmpgebin, "-i", input, output)
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	err := cmd.Run()
	out := stdout.String() + stderr.String()
	if err != nil {
		return "", "", fmt.Errorf("while converting %v to mp4: %v", input, out)
	}
	return out, output, nil
}

func monitorTemperature(ctx context.Context) {
	interval := time.Second
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			data, err := os.ReadFile("/sys/devices/virtual/thermal/thermal_zone0/temp")
			if err != nil {
				log.Printf("while reading temperature: %v", err)
			} else {
				log.Printf("temperature: %s", string(data))
			}
			time.Sleep(interval)
			// TODO: add watchdog?
		}
	}()
}
