// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package utils

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/rpc"
	"net/url"
	"strings"
	"time"

	"github.com/mpl/basicauth"
)

var DashLogURL = "http://localhost:7144/logs/"

// LogAndDash writes s both to lg (synchronously), and to the local
// dashboard (asynchronously).
func LogAndDash(lg *log.Logger, s string) {
	var buf bytes.Buffer
	// Creating a new logger on the fly, because we want the same
	// timestamp, prefix, etc on the local log line, and on the one we
	// post to the dashboard.
	log.New(io.MultiWriter(lg.Writer(), &buf), lg.Prefix(), lg.Flags()).Printf(s)
	go func() {
		if err := logToDash(buf.String()); err != nil {
			log.Printf("while logging to data browser: %v", err)
		}
	}()
}

func logToDash(s string) error {
	if DashLogURL == "" {
		// TODO: log about it? ironic isn't it?
		return nil
	}
	req, err := http.NewRequest("POST", DashLogURL,
		strings.NewReader(url.Values{"v": []string{s}}.Encode()))
	if err != nil {
		return fmt.Errorf("while creating request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	up, err := basicauth.NewUserPass("")
	if err != nil {
		return err
	}
	req.SetBasicAuth(up.U, up.P)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("while posting log: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	return nil
}

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
