// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"tailscale.com/net/ping"
	"tailscale.com/types/logger"
)

var (
	flagMbimNet     string
	flagMbimProfile string
	flagPin         string
	flagPriority    int
	flagDebug       bool
	flagVerbose     bool
	flagHost        string
	flagDummy       bool
)

func init() {
	flag.StringVar(&flagMbimNet, "mbimnet", mbimNetwork, "path to the mbim-network script")
	flag.StringVar(&flagMbimProfile, "profile", mbimConf, "path to the mbim-network.conf profile file")
	flag.StringVar(&flagPin, "pin", "", "SIM card pin code")
	flag.IntVar(&flagPriority, "priority", -2, "dhcp route priority")
	flag.BoolVar(&flagDebug, "debug", false, "run in debug mode (shorter timeouts)")
	flag.BoolVar(&flagVerbose, "v", true, "be verbose")
	flag.StringVar(&flagHost, "host", "localhost:3399", "our own HTTP server to report on status")
	flag.BoolVar(&flagDummy, "dummy", false, "Only run a status handler that says OK all the time.")
}

const (
	// TODO: "port" mbim-network to Go
	mbimNetwork = "/usr/local/bin/mbim-network"
	mbimConf    = "/etc/mbim-network.conf"
)

var (
	statusMu sync.RWMutex
	status   bool
)

func checkFlags() {
	if flagDummy {
		return
	}
	if flagPin == "" {
		log.Fatal("-pin for SIM card not provided")
	}
}

func main() {
	flag.Parse()

	checkFlags()

	http.HandleFunc("/status", statusHandler)
	go func() {
		log.Fatal(http.ListenAndServe(flagHost, nil))
	}()

	if flagDummy {
		statusMu.Lock()
		status = true
		statusMu.Unlock()
		select {}
	}

	retryConnect()

	go func() {
		if err := dhcp(); err != nil {
			log.Fatal(err)
		}
	}()

	// let DHCP do its thing
	time.Sleep(time.Minute)

	if err := retryTestConn(); err != nil {
		log.Fatal(err)
	}
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	statusMu.RLock()
	defer statusMu.RUnlock()
	if status {
		w.Write([]byte("OK"))
		return
	}
	w.Write([]byte("KO"))
}

func retryConnect() {
	minPause := time.Second
	maxPause := 5 * time.Minute
	pause := minPause
	for {
		err := connect()
		if err == nil {
			return
		}
		log.Printf("while trying to connect with cellular: %v\nretrying in %f", err, pause.Seconds())
		time.Sleep(pause)
		pause *= 2
		if pause > maxPause {
			pause = minPause
		}
	}
}

func connect() error {
	var out []byte
	var err error
	if out, err = mbim(); err != nil {
		return fmt.Errorf("%v: %v", err, string(out))
	}
	if !strings.Contains(string(out), "Network started successfully") {
		return fmt.Errorf("Failed to connect: %s", string(out))
	}

	return nil
}

func mbim() ([]byte, error) {
	cmd := exec.Command(flagMbimNet, "--pin", flagPin, "--profile", flagMbimProfile, "/dev/cdc-wdm0", "start")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	fmt.Fprintf(os.Stdout, "%s", string(out))
	return out, err
}

func retryTestConn() error {
	bigPause := time.Minute
	if flagDebug {
		bigPause = 30 * time.Second
	}
	for {
		if err := checkValidAddr(); err != nil {
			return fmt.Errorf("while checking for valid address from DHCP: %v", err)
		}

		pause := 30 * time.Second
		timeout := 10 * time.Minute
		if flagDebug {
			timeout = 2 * time.Minute
		}
		start := time.Now()
		for {
			ok, err := testConnection()
			if err != nil {
				return fmt.Errorf("while testing connection: %v", err)
			}
			if ok {
				break
			}
			time.Sleep(pause)
			if time.Since(start) > timeout {
				return fmt.Errorf("network unreachable for more than %f, restarting cellular", timeout.Seconds())
			}
			pause *= 2
			if flagDebug {
				pause = 30 * time.Second
			}
		}
		statusMu.Lock()
		status = true
		statusMu.Unlock()
		time.Sleep(bigPause)
		statusMu.Lock()
		status = false
		statusMu.Unlock()
	}
}

func checkValidAddr() error {
	// TODO: I'm sure there's a Go native way to get that info too, but going the easy way for now.
	cmd := exec.Command("/usr/local/bin/busybox", "ifconfig", "wwan0")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	strout := string(out)
	if err != nil {
		return fmt.Errorf("%v: %v", err, strout)
	}
	const inetPrefix = "inet addr:"
	var addr string
	sc := bufio.NewScanner(strings.NewReader(strout))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(l, inetPrefix) {
			continue
		}
		l = strings.TrimPrefix(l, inetPrefix)
		parts := strings.Split(l, " ")
		if len(parts) < 2 {
			return fmt.Errorf("unexpected number of parts: %d", len(parts))
		}
		addr = parts[0]
		break
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if addr == "" {
		return errors.New("inet addr not found")
	}
	if ip := net.ParseIP(addr); ip == nil {
		return fmt.Errorf("invalid IP address: %v", addr)
	}
	if strings.HasPrefix(addr, "192.0") {
		return errors.New("DHCP did not give us working address")
	}
	log.Printf("Got valid addr from DHCP: %v", addr)
	return nil
}

// TODO: remove error from signature when we're happy with this implementation.
// TODO: maybe use tailscale.com/health instead of all that?
func testConnection() (bool, error) {
	dest, err := net.ResolveIPAddr("ip4", "google.com")
	if err != nil {
		log.Printf("while testing connection, cannot resolve: %v", err)
		return false, nil
	}

	// TODO: do we actually need all of that below? Could we just limit
	// ourselves to try resolving?
	parentCtx := context.Background()
	var lg logger.Logf
	var lp net.ListenConfig
	p := ping.New(parentCtx, lg, &lp)
	defer p.Close()

	count := 20
	tm := 10 * time.Second
	badPing := 0
	lastPingOK := true
	for i := 0; i < count; i++ {
		ctx, cancel := context.WithTimeout(parentCtx, tm)
		d, err := p.Send(ctx, dest, nil)
		cancel()
		if err != nil {
			badPing++
			lastPingOK = false
			if flagVerbose {
				log.Printf("while testing connection with ping: %v", err)
			}
		} else {
			lastPingOK = true
			if flagVerbose {
				log.Printf("ping ok: %fs", d.Seconds())
			}
		}
		time.Sleep(3 * time.Second)
	}
	// if last ping was good, we're happy
	if lastPingOK {
		return true, nil
	}
	// if last ping was not good, but most of our pings were good, we're still happy
	badPingRatio := float64(badPing) / float64(count)
	if badPingRatio <= 0.25 {
		return true, nil
	}
	if flagVerbose {
		log.Printf("bad pings ratio: %f", badPingRatio)
	}
	return false, nil
}
