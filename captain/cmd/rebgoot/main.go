// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

//go:build linux && arm64

package main

import "golang.org/x/sys/unix"

func main() {
	unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART)
}
