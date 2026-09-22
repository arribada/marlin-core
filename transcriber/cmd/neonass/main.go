// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"fmt"
	"runtime"
	"sync"
)

// Assembly function declaration implemented in neon_arm64.s
func neonBurnLoop()

func worker(id int, wg *sync.WaitGroup) {
	defer wg.Done()
	// Lock this goroutine to a dedicated OS thread for maximum CPU saturation
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Enter the infinite 128-bit NEON assembly loop
	neonBurnLoop()
}

func main() {
	cores := runtime.NumCPU()
	runtime.GOMAXPROCS(cores)

	fmt.Printf("[+] Firing Pure Go NEON 128-bit Vector Burst across %d cores...\n", cores)

	var wg sync.WaitGroup
	for i := 0; i < cores; i++ {
		wg.Add(1)
		go worker(i, &wg)
	}

	wg.Wait()
}
