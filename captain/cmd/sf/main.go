// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"path/filepath"
)

var (
	flagDataDir string
	flagCount   int
)

func init() {
	flag.StringVar(&flagDataDir, "datadir", "/perm/marlin/.rand", "where to write the filler files")
	flag.IntVar(&flagCount, "n", 0, "where to start the file counter")
}

func main() {
	flag.Parse()

	if err := os.MkdirAll(flagDataDir, 0700); err != nil {
		log.Fatal(err)
	}

	var tinyData []byte
	for i := 0; i < 1e6; i++ {
		n := rand.Int()
		tinyData = append(tinyData, []byte(fmt.Sprintf("%d", n))...)
	}

	var smallData []byte
	for i := 0; i < 10; i++ {
		smallData = append(smallData, tinyData...)
	}

	var bigData []byte
	for i := 0; i < 10; i++ {
		bigData = append(bigData, smallData...)
	}

	println("BIG: ", len(bigData))
	n := 0
	// in := hugeFile(bigData)
	// n = copyFiles(flagDataDir, in)

	for {
		fullPath := filepath.Join(flagDataDir, fmt.Sprintf("big-%d.dat", n))
		if err := os.WriteFile(fullPath, bigData, 0600); err != nil {
			log.Printf("big: %v", err)
			break
		}
		n++
	}
	for {
		fullPath := filepath.Join(flagDataDir, fmt.Sprintf("small-%d.dat", n))
		if err := os.WriteFile(fullPath, smallData, 0600); err != nil {
			log.Printf("small: %v", err)
			break
		}
		n++
	}
	for {
		fullPath := filepath.Join(flagDataDir, fmt.Sprintf("tiny-%d.dat", n))
		if err := os.WriteFile(fullPath, tinyData, 0600); err != nil {
			log.Printf("tiny: %v", err)
			break
		}
		n++
	}
}

func hugeFile(bigData []byte) string {
	fullPath := filepath.Join(flagDataDir, "huge.dat")
	f, err := os.Create(fullPath)
	if err != nil {
		log.Fatal(err)
	}

	// TODO: limited by FAT for now
	//	for i := 0; i < 5; i++ {
	for i := 0; i < 2; i++ {
		if _, err := f.Write(bigData); err != nil {
			log.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}

	return fullPath
}

func copyFiles(flagDataDir, in string) int {
	n := 0
	for {
		f, err := os.Open(in)
		if err != nil {
			log.Fatal(err)
		}
		fullPath := filepath.Join(flagDataDir, fmt.Sprintf("huge-%d.dat", n))
		g, err := os.Create(fullPath)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := io.Copy(g, f); err != nil {
			log.Printf("copy huge: %v", err)
			f.Close()
			g.Close()
			break
		}
		f.Close()
		if err := g.Close(); err != nil {
			log.Printf("close huge: %v", err)
			break
		}
		n++
	}
	return n
}
