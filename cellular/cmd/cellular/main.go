// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

var (
	flagMbimNet     string
	flagMbimProfile string
	flagPin         string
	flagPriority    int
)

func init() {
	flag.StringVar(&flagMbimNet, "mbimnet", mbimNetwork, "path to the mbim-network script")
	flag.StringVar(&flagMbimProfile, "profile", mbimConf, "path to the mbim-network.conf profile file")
	flag.StringVar(&flagPin, "pin", "1234", "SIM card pin code")
	flag.IntVar(&flagPriority, "priority", -2, "dhcp route priority")
}

const (
	// TODO: "port" mbim-network to Go
	mbimNetwork = "/usr/local/bin/mbim-network"
	mbimConf    = "/etc/mbim-network.conf"
)

func checkFlags() {}

func main() {
	flag.Parse()

	checkFlags()

	if err := connect(); err != nil {
		log.Fatal(err)
	}

	go func() {
		// check regularly whether DHCP gave us a valid address
		for {
			time.Sleep(2 * time.Minute)
			if err := checkValidAddr(); err != nil {
				log.Fatal(err)
			}
		}
	}()

	if err := dhcp(); err != nil {
		log.Fatal(err)
	}
}

func dhcp() error {
	// TODO: be stricter about parsing priority
	cmd := exec.Command("/gokrazy/dhcp", "-interface=wwan0", fmt.Sprintf("-extra_route_priority=%d", flagPriority))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Wait()
}

func mbim() ([]byte, error) {
	cmd := exec.Command(flagMbimNet, "--pin", flagPin, "--profile", flagMbimProfile, "/dev/cdc-wdm0", "start")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	fmt.Fprintf(os.Stdout, "%s", string(out))
	return out, err
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
	println("GOOD ADDR:", addr)
	return nil
}
