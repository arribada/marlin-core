// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

//go:build linux

package main

import (
	"os"
	"syscall"
)

// TODO: maybe mount by label? not very helpful since we needed device name anyway for prepping.
func mount(device, mntPoint string) error {
	if err := os.MkdirAll(mntPoint, 0755); err != nil {
		return err
	}

	// TODO: investigate all the possible options
	return syscall.Mount(device, mntPoint, "vfat", 0, "umask=0000")
}
