// Copyright (c) The Arribada initiative.
// Licensed under the MIT License.

package utils

import (
	"fmt"
	"log"
	"net/rpc"
	"time"
)

func WaitForDrive(addr string) error {
	log.Printf("dialing drive prepper at %v", addr)
	var client *rpc.Client
	for {
		var err error
		client, err = rpc.DialHTTP("tcp", addr)
		if err == nil {
			defer client.Close()
			break
		}
		time.Sleep(time.Second)
	}

	log.Printf("waiting for drive prepper to be done")
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
		time.Sleep(time.Second)
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
