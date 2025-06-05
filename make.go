// Copyright (c) The Arribada initiative.
// Licensed under the MIT License.

package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	// TODO: make it a list
	flagVessel  string
	flagGoproxy string
)

func init() {
	flag.StringVar(&flagVessel, "vessel", "", "vessel to update")
	flag.StringVar(&flagGoproxy, "goproxy", "direct", "to set GOPROXY during the build in docker. Use 'make athens' to start a local one beforehand. then run with e.g. http://192.168.0.47:3000")
}

var (
	allVessels = []string{
		"marlin-augnat1",
		"marlin-augnat2",
		"marlin-augnat3",
	}
	vessels []string
)

func checkVessels() error {
	if flagVessel == "" {
		vessels = allVessels
		return nil
	}
	for _, v := range allVessels {
		if flagVessel == v {
			configPath := filepath.Join("gokrazy", v, "config.json")
			if _, err := os.Stat(configPath); err != nil {
				return fmt.Errorf("%v is a known vessel, but no config file found", v)
			}
			vessels = []string{flagVessel}
			return nil
		}
	}
	return errors.New("unexpected provided vessel")
}

func main() {
	flag.Parse()

	if err := checkVessels(); err != nil {
		log.Fatal(err)
	}

	if err := os.Setenv("GOPROXY", flagGoproxy); err != nil {
		log.Fatal(err)
	}
	for _, v := range vessels {
		if err := os.Setenv("INSTANCE", v); err != nil {
			log.Fatal(err)
		}
		if err := updateVessel(v); err != nil {
			log.Printf("failed to update vessel %v: %v", v, err)
		}
	}
}

// TODO: use templates? meh.
func injectSecrets(vessel string) ([]byte, error) {
	secretVars := filepath.Join("gokrazy", vessel, "secrets", "vars.txt")
	data, err := os.ReadFile(secretVars)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	secrets := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewBuffer(data))
	for sc.Scan() {
		l := sc.Text()
		parts := strings.SplitN(l, "=", 2)
		if len(parts) != 2 {
			return nil, errors.New("unexpected number of parts")
		}
		secrets[parts[0]] = parts[1]
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	cfgFile := filepath.Join("gokrazy", vessel, "config.json")
	cfg, err := os.ReadFile(cfgFile)
	if err != nil {
		return nil, err
	}

	newCfg := make([]byte, len(cfg))
	copy(newCfg, cfg)
	for k, v := range secrets {
		newCfg = bytes.Replace(newCfg, []byte(k), []byte(v), 1)
	}

	return cfg, os.WriteFile(cfgFile, newCfg, 0644)
}

func restoreConfig(vessel string, ini []byte) error {
	cfgFile := filepath.Join("gokrazy", vessel, "config.json")
	return os.WriteFile(cfgFile, ini, 0644)
}

func updateVessel(vessel string) error {
	ini, err := injectSecrets(vessel)
	defer func() {
		if ini == nil {
			log.Printf("Config file was untouched, no need to restore it.")
			return
		}
		if err := restoreConfig(vessel, ini); err != nil {
			log.Printf("While restoring config file: %v", err)
		}
	}()
	if err != nil {
		log.Fatalf("while injecting secrets: %v", err)
	}
	cmd := exec.Command("make", "update")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}
