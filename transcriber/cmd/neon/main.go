// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"time"
)

// Simulates dense float32 matrix multiplications (similar to neural net inference)
func vectorWorker(wg *sync.WaitGroup) {
	defer wg.Done()

	const size = 256
	A := make([]float32, size*size)
	B := make([]float32, size*size)
	C := make([]float32, size*size)

	// Fill with dummy float weights
	for i := range A {
		A[i] = rand.Float32()
		B[i] = rand.Float32()
	}

	// Continuous dense GEMM matrix multiplication loop
	for {
		for i := 0; i < size; i++ {
			for j := 0; j < size; j++ {
				var sum float32
				for k := 0; k < size; k++ {
					sum += A[i*size+k] * B[k*size+j]
				}
				C[i*size+j] = sum
			}
		}
	}
}

func main() {
	cores := runtime.NumCPU()
	runtime.GOMAXPROCS(cores)

	fmt.Printf("[+] Starting Go Matrix Power Stress on %d cores...\n", cores)

	var wg sync.WaitGroup
	for i := 0; i < cores; i++ {
		wg.Add(1)
		go vectorWorker(&wg)
	}

	// Keep main thread alive
	time.Sleep(10 * time.Minute)
}