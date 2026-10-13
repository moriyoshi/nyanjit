// Command gcmap reproduces a Go/QEMU GC-checker failure without importing
// NyanJIT, purego, unsafe, or any nonstandard package. Run with GOGC=10 and
// GODEBUG=gccheckmark=1. Failure is intermittent; a pass is not a resolution.
package main

import (
	"fmt"
	"runtime"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	for worker := 0; worker < 20; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			roots := make([]map[int]map[int]int, 64)
			for iteration := 0; iteration < 20000; iteration++ {
				outer := map[int]map[int]int{}
				inner := make(map[int]int)
				outer[0] = inner
				for j := 0; j < 14; j++ {
					inner[j] = worker + iteration + j
				}
				roots[iteration%len(roots)] = outer
			}
			for _, outer := range roots {
				if len(outer[0]) != 14 {
					panic("bad map")
				}
			}
			runtime.KeepAlive(roots)
		}(worker)
	}
	wg.Wait()
	runtime.GC()
	fmt.Println("PASS: 400000 nested maps, 20 workers")
}
