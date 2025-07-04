// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

var (
	flagMbimNet     string
	flagMbimProfile string
	flagPin         string
)

func init() {
	flag.StringVar(&flagMbimNet, "mbimnet", mbimNetwork, "path to the mbim-network script")
	flag.StringVar(&flagMbimProfile, "profile", mbimConf, "path to the mbim-network.conf profile file")
	flag.StringVar(&flagPin, "pin", "1234", "SIM card pin code")
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

	if err := dhcp(); err != nil {
		log.Fatal(err)
	}
}

func dhcp() error {
	cmd := exec.Command("/gokrazy/dhcp", "-interface=wwan0", "-extra_route_priority=5")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func mbim() ([]byte, error) {
	cmd := exec.Command(flagMbimNet, "--pin", flagPin, "--profile", flagMbimProfile, "/dev/cdc-wdm0", "start")
	//	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	//	return cmd.Run()
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
