package core

import (
	"os"
	"path/filepath"
	"testing"
)

// CPU profiling is a process-wide singleton. When a second traced call tries to
// start one while another is running, pprof refuses -- and that refusal used to
// be discarded, so the caller recorded a profile path pointing at the zero-byte
// file os.Create had just truncated.
func TestConcurrentCPUProfileDoesNotLeaveATruncatedFile(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.prof")
	second := filepath.Join(dir, "second.prof")

	f, err := StartCPUProfile(first)
	if err != nil {
		t.Fatalf("first profile should start: %v", err)
	}
	defer StopCPUProfile(f)

	// Second one must be refused, not silently accepted.
	f2, err := StartCPUProfile(second)
	if err == nil {
		StopCPUProfile(f2)
		t.Fatal("second concurrent CPU profile reported success; pprof only " +
			"supports one at a time, so this would have handed back a handle " +
			"to a file the first profile is writing")
	}
	if f2 != nil {
		t.Error("a failed start returned a non-nil file handle")
	}
	if _, statErr := os.Stat(second); !os.IsNotExist(statErr) {
		t.Errorf("the truncated file was left on disk (%v); it looks like a "+
			"profile to anything that finds it", statErr)
	}
}
