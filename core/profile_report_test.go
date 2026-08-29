package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"time"
)

// writeCPUProfile captures a real profile of some busy work and returns its
// path. Real profiles rather than fixtures: the whole claim under test is that
// what runtime/pprof writes is self-describing.
func writeCPUProfile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cpu.prof")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := pprof.StartCPUProfile(f); err != nil {
		t.Skipf("CPU profiling unavailable: %v", err)
	}
	// Long enough to catch samples at the profiler's 100 Hz.
	deadline := time.Now().Add(300 * time.Millisecond)
	x := 0.0
	for time.Now().Before(deadline) {
		for i := 0; i < 200000; i++ {
			x += float64(i % 7)
		}
	}
	pprof.StopCPUProfile()
	_ = x
	return path
}

// TestReportsRenderWithoutTheGoToolchain is the point of the change.
//
// The previous implementation ran `go tool pprof`, so on any image without the
// Go SDK -- distroless, scratch, alpine -- the dashboard showed
// "Error: 'go' command not found" where the profile should be. Emptying PATH
// reproduces that environment: if anything still shells out, this fails.
func TestReportsRenderWithoutTheGoToolchain(t *testing.T) {
	path := writeCPUProfile(t)

	t.Setenv("PATH", "")
	if _, err := exec.LookPath("go"); err == nil {
		t.Skip("go is still resolvable with an empty PATH on this system")
	}

	report := renderProfileReport(path, "text")
	if strings.Contains(report, "command not found") || strings.Contains(report, "Error") {
		t.Fatalf("report reads as an error with no toolchain:\n%s", report)
	}
	if !strings.Contains(report, "Type: cpu") {
		t.Errorf("no profile type header:\n%s", report)
	}
	// The function under test must appear by name, from the profile alone --
	// no binary and no source tree were consulted.
	if !strings.Contains(report, "monigo/core.writeCPUProfile") {
		t.Errorf("the profiled function is not named in the report:\n%s", report)
	}
}

func TestEveryReportTypeRenders(t *testing.T) {
	path := writeCPUProfile(t)
	for _, rt := range []string{"text", "tree", "traces"} {
		t.Run(rt, func(t *testing.T) {
			got := renderProfileReport(path, rt)
			if got == "" {
				t.Fatal("empty report")
			}
			if strings.Contains(got, "Invalid report type") {
				t.Fatalf("%q was rejected: %s", rt, got)
			}
			if !strings.Contains(got, "Type: cpu") {
				t.Errorf("missing header:\n%s", got)
			}
		})
	}
}

func TestUnknownReportTypeIsNamed(t *testing.T) {
	path := writeCPUProfile(t)
	got := renderProfileReport(path, "flamegraph")
	if !strings.Contains(got, "Invalid report type") || !strings.Contains(got, "flamegraph") ||
		!strings.Contains(got, "Supported") {
		t.Errorf("an unknown type should say so and list the alternatives, got:\n%s", got)
	}
}

// An empty profile is the common case, not an edge case: the CPU profiler
// samples at 100 Hz, so a sub-10ms call usually records nothing. A bare empty
// table would invite the reader to conclude the function is free.
func TestAnEmptyProfileExplainsItself(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.prof")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		t.Skipf("CPU profiling unavailable: %v", err)
	}
	pprof.StopCPUProfile()
	f.Close()

	got := renderProfileReport(path, "text")
	if !strings.Contains(got, "No samples") {
		t.Errorf("an empty profile should say so:\n%s", got)
	}
	if !strings.Contains(got, "does not mean the function used no CPU") {
		t.Errorf("an empty profile should explain why it is empty:\n%s", got)
	}
}

func TestMissingProfileIsNotAnError(t *testing.T) {
	got := renderProfileReport(filepath.Join(t.TempDir(), "nope.prof"), "text")
	if !strings.Contains(got, "Profiles are written only for sampled calls") {
		t.Errorf("an absent profile should explain sampling, got: %s", got)
	}
	if got := renderProfileReport("", "text"); !strings.Contains(got, "No profile") {
		t.Errorf("an empty path should say no profile was captured, got: %s", got)
	}
}

func TestCorruptProfileReportsTheParseFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk.prof")
	if err := os.WriteFile(path, []byte("this is not a pprof profile"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := renderProfileReport(path, "text"); !strings.Contains(got, "Could not parse") {
		t.Errorf("expected a parse failure, got: %s", got)
	}
}

// Annotated source must degrade to line numbers rather than failing outright:
// the per-line counts come from the profile and are always available, while
// the source text is only present on a machine that has the tree.
func TestAnnotatedSourceWorksWithoutTheSourceTree(t *testing.T) {
	path := writeCPUProfile(t)
	got := renderAnnotatedSource(path, "github.com/iyashjayesh/monigo/core.writeCPUProfile")
	if !strings.Contains(got, "Function:") {
		t.Fatalf("no function header:\n%s", got)
	}
	// This machine has the source, so the real lines should appear.
	if !strings.Contains(got, "deadline") && !strings.Contains(got, "Source is not available") {
		t.Errorf("neither annotated source nor the fallback appeared:\n%s", got)
	}
}

func TestRecursionCountsOnceTowardCumulative(t *testing.T) {
	// A function appearing twice in one stack must not have its own cumulative
	// total doubled by its own recursive frame.
	path := writeCPUProfile(t)
	report := renderProfileReport(path, "text")
	for _, line := range strings.Split(report, "\n") {
		if !strings.Contains(line, "monigo/core.") {
			continue
		}
		// cum% is the fifth column; nothing may exceed 100%.
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		cumPct := strings.TrimSuffix(fields[4], "%")
		if cumPct != "" && len(cumPct) > 0 {
			if v := parseFloat(cumPct); v > 100.01 {
				t.Errorf("cumulative %.2f%% exceeds the total in: %s", v, line)
			}
		}
	}
}

func parseFloat(s string) float64 {
	var f float64
	var neg bool
	i := 0
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		neg = s[i] == '-'
		i++
	}
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		f = f*10 + float64(s[i]-'0')
	}
	if i < len(s) && s[i] == '.' {
		i++
		scale := 0.1
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			f += float64(s[i]-'0') * scale
			scale /= 10
		}
	}
	if neg {
		return -f
	}
	return f
}
