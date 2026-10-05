//go:build tinygo && esp32s3

package esp32s3

import (
	"runtime"
	"time"
)

// MemoryDiagnostics is opt-in, prints counters only, and adds no goroutine.
var MemoryDiagnostics = "false"
var lastMemoryReport time.Time

func memorySnapshot(stage string) {
	if MemoryDiagnostics != "true" {
		return
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	println("MEM:", stage, "heap=", m.HeapSys, "alloc=", m.HeapAlloc,
		"idle=", m.HeapIdle, "objects=", m.HeapObjects, "total=", m.TotalAlloc,
		"mallocs=", m.Mallocs, "frees=", m.Frees, "gc=", m.NumGC)
}

func memoryHeartbeat() {
	if MemoryDiagnostics == "true" && time.Since(lastMemoryReport) >= time.Second {
		lastMemoryReport = time.Now()
		memorySnapshot("session")
	}
}
