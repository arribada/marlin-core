// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package utils

import (
	"errors"
	"fmt"
	"log"
	"net/rpc"
	"time"
)

func WaitForDrive(addr string, d time.Duration) error {
	log.Printf("dialing drive prepper at %v", addr)
	var client *rpc.Client
	// TODO: allow for negative duration -> infinite retries
	deadline := time.Now().Add(d)
	pause := time.Second
	maxPause := 5 * time.Minute
	for {
		var err error
		client, err = rpc.DialHTTP("tcp", addr)
		if err == nil {
			defer client.Close()
			break
		}
		time.Sleep(pause)
		pause *= 2
		if pause > maxPause {
			pause = maxPause
		}
		if time.Now().After(deadline) {
			return errors.New("giving up on reaching drive prepper")
		}
	}

	log.Printf("waiting for drive prepper to be done")
	pause = time.Second
	maxPause = 5 * time.Minute
	for {
		var reply string
		err := client.Call("DrivePrepper.IsReady", 0, &reply)
		if err != nil {
			return fmt.Errorf("DrivePrepper error: %v", err)
		}
		if reply == "ready" {
			break
		}
		if reply != "" {
			log.Printf("error from drive captain: %v", reply)
		}
		time.Sleep(pause)
		pause *= 2
		if pause > maxPause {
			pause = maxPause
		}
		if time.Now().After(deadline) {
			return errors.New("giving up on waiting for drive")
		}
	}
	log.Printf("drive ready to be used")
	return nil
}

func MaybeWrap(newErr, oldErr error, msg string) error {
	newErr = fmt.Errorf("%s: %v", msg, newErr)
	if oldErr == nil {
		return newErr
	}
	return fmt.Errorf("%v, %w", newErr, oldErr)
}
