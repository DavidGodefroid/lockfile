// Answers 1 once it holds 1500 threads at the same time (each goroutine
// locked to its own thread). Under a thread cap the runtime cannot create
// them and the plugin dies.
package main

import (
	"os"
	"runtime"
	"sync"
)

const want = 1500

func main() {
	var started sync.WaitGroup
	block := make(chan struct{})
	for i := 0; i < want; i++ {
		started.Add(1)
		go func() {
			runtime.LockOSThread()
			started.Done()
			<-block
		}()
	}
	started.Wait()
	os.Stdout.WriteString("1")
}
