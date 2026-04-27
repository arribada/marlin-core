// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func dhcp() error {
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
