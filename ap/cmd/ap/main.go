// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gokrazy/gokrazy"
	"github.com/mdlayher/wifi"
	"golang.org/x/sys/unix"
)

var (
	hostapdConf string
)

func main() {
	flag.Parse()

	if err := checkConf(); err != nil {
		log.Fatal(err)
	}

	if err := loadModules(); err != nil {
		log.Fatal(err)
	}

	if err := checkInterface(); err != nil {
		log.Fatal(err)
	}

	if err := setupInterface(); err != nil {
		log.Fatal(err)
	}

	go func() {
		if err := startHostapd(); err != nil {
			log.Fatal(err)
		}
		time.Sleep(10 * time.Second)
	}()

	if err := startDHCPD(); err != nil {
		log.Fatal(err)
	}
}

func checkConf() error {
	if _, err := os.Stat("/perm/hostapd.conf"); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if _, err := os.Stat("/etc/hostapd.conf"); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			gokrazy.DontStartOnBoot()
		} else {
			hostapdConf = "/etc/hostapd.conf"
		}
	} else {
		hostapdConf = "/perm/hostapd.conf"
	}
	return nil
}

func loadModules() error {
	// modprobe the brcmfmac driver
	for _, mod := range []string{
		"kernel/drivers/net/wireless/broadcom/brcm80211/brcmutil/brcmutil.ko",
		"kernel/drivers/net/wireless/broadcom/brcm80211/brcmfmac/brcmfmac.ko",
		"kernel/drivers/net/wireless/broadcom/brcm80211/brcmfmac/wcc/brcmfmac-wcc.ko",
	} {
		if err := loadModule(mod); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func release() string {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		fmt.Fprintf(os.Stderr, "minitrd: %v\n", err)
		os.Exit(1)
	}
	return string(uts.Release[:bytes.IndexByte(uts.Release[:], 0)])
}

func loadModule(mod string) error {
	f, err := os.Open(filepath.Join("/lib/modules", release(), mod))
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.FinitModule(int(f.Fd()), "", 0); err != nil {
		if err != unix.EEXIST &&
			err != unix.EBUSY &&
			err != unix.ENODEV &&
			err != unix.ENOENT {
			return fmt.Errorf("FinitModule(%v): %v", mod, err)
		}
	}
	return nil
}

func checkInterface() error {
	cl, err := wifi.New()
	if err != nil {
		return err
	}
	interfaces, err := cl.Interfaces()
	if err != nil {
		return err
	}
	if len(interfaces) == 0 {
		return fmt.Errorf("no interfaces found")
	}

	return nil
}

func setupInterface() error {
	// TODO: do I need to clean it up beforehand, from previous failed runs?
	cmd := exec.Command("/usr/local/bin/busybox", "ip", "addr",
		"add", "192.168.1.1/24",
		"broadcast", "192.168.1.255",
		"dev", "wlan0")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("while setting up interface: %v", err)
	}
	return nil
}

func startHostapd() error {
	cmd := exec.Command("/usr/local/lib/hostapd/ld-linux-aarch64.so.1", "/usr/local/bin/hostapd", hostapdConf)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
	}
	cmd.Env = []string{
		"LD_LIBRARY_PATH=/usr/local/lib/hostapd",
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Wait()
}

var intfTemplate = `{
	"interfaces": [
		{
			"hardware_addr": "%v",
			"name": "wlan0",
			"addr": "192.168.1.1/24"
		}
	]
}
`

func startDHCPD() error {
	b, err := os.ReadFile("/sys/class/net/wlan0/address")
	if err != nil {
		return fmt.Errorf("while reading /sys/class/net/wlan0/address: %v", err)
	}
	hwAddr := strings.TrimSpace(string(b))

	data := fmt.Sprintf(intfTemplate, hwAddr)
	if err := os.WriteFile("/perm/interfaces.json", []byte(data), 0644); err != nil {
		return errors.New("while creating /perm/interfaces.json")
	}

	cmd := exec.Command("/user/dhcp4d", "-interface=wlan0")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
