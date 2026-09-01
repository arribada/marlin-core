// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	flagUser     string
	flagPassword string
	flagVerbose  bool
	flagAddr     string
	flagService  string
	flagPause    int
)

func init() {
	flag.StringVar(&flagUser, "user", "gokrazy", "basic auth username")
	flag.StringVar(&flagPassword, "password", "", "basic auth password")
	flag.StringVar(&flagAddr, "addr", "http://localhost:80", "address")
	flag.StringVar(&flagService, "svc", "", "service to restart")
	flag.BoolVar(&flagVerbose, "v", true, "be verbose")
	flag.IntVar(&flagPause, "pause", 1, "pause in seconds between stop and restart")
}

func checkConf() {
	if flagUser == "" {
		log.Fatal("empty -user")
	}
	if flagPassword == "" {
		log.Fatal("empty -password")
	}
	if flagAddr == "" {
		log.Fatal("empty -addr")
	}
	if flagService == "" {
		log.Fatal("empty -svc")
	}
}

func main() {
	flag.Parse()

	checkConf()

	if err := restartService(flagService); err != nil {
		log.Fatal(err)
	}
}

func restartService(serviceName string) error {
	ck, err := fetchCookies(serviceName)
	if err != nil {
		return err
	}

	start := false
	if err := toggleService(serviceName, ck, start); err != nil {
		return err
	}
	fmt.Println(serviceName, "stopped")

	time.Sleep(time.Duration(flagPause) * time.Second)
	start = true
	if err := toggleService(serviceName, ck, start); err != nil {
		return err
	}
	fmt.Println(serviceName, "restarted")

	return nil
}

func fetchCookies(serviceName string) (*http.Cookie, error) {
	req, err := http.NewRequest("GET", flagAddr+"/status?path="+serviceName, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(flagUser, flagPassword)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%d", resp.StatusCode)
	}

	for _, v := range resp.Cookies() {
		if v.Name == "gokrazy_xsrf" {
			return v, nil
		}
	}

	return nil, errors.New("Cookie not found")
}

func toggleService(serviceName string, ck *http.Cookie, start bool) error {
	params := fmt.Sprintf("?path=%s", serviceName)
	values := url.Values{"xsrftoken": {ck.Value}}
	var urlPath string
	if start {
		urlPath = "/restart"
	} else {
		urlPath = "/stop"
	}

	// TODO: handle start once
	req, err := http.NewRequest("POST", flagAddr+urlPath+params, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth(flagUser, flagPassword)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("%v", err)
		} else {
			println(string(data))
		}
		return fmt.Errorf("%d", resp.StatusCode)
	}

	return nil
}
