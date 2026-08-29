package core

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/pprof/profile"
)

// In-process pprof rendering.
//
// This replaces `exec.Command("go", "tool", "pprof", ...)`. That required the
// Go SDK on PATH at runtime, so on any distroless, scratch or alpine image --
// which is to say nearly every production deployment of a Go service -- the
// dashboard's profile view showed an error string instead of a profile. It
// also spawned a subprocess per HTTP request.
//
// None of that was necessary. Profiles written by runtime/pprof are fully
// symbolized: the function names and source paths are already inside the file.
// Parsing one needs no binary, no source tree and no toolchain.

// maxReportNodes caps how many rows a report renders. A deep profile can hold
// thousands of functions, and the dashboard puts this in a <pre> block; the
// tail of that list is noise nobody scrolls to.
const maxReportNodes = 100

// renderProfileReport reads a pprof profile and renders it as text.
//
// Errors are returned as the report body rather than as an error value,
// because that is what the endpoint shows the operator: a profile that cannot
// be read is a thing to display, not a request failure.
func renderProfileReport(path, reportType string) string {
	// Validate the report type before looking at the file, and reject an empty
	// one. The API layer defaults "" to "text" before it reaches here, so an
	// empty value arriving at this function means a caller bypassed that --
	// which is worth rejecting rather than quietly guessing.
	switch reportType {
	case "text", "tree", "traces":
	default:
		return fmt.Sprintf("Error: Invalid report type %q. Supported: text, tree, traces.", reportType)
	}

	if path == "" {
		return "No profile was captured for this function."
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "No profile on disk. Profiles are written only for sampled calls."
		}
		return fmt.Sprintf("Could not open the profile: %v", err)
	}
	defer f.Close()

	p, err := profile.Parse(f)
	if err != nil {
		return fmt.Sprintf("Could not parse the profile: %v", err)
	}

	switch reportType {
	case "tree":
		return renderTree(p)
	case "traces":
		return renderTraces(p)
	default: // "text", already validated above
		return renderTop(p)
	}
}

// valueIndex picks which of a sample's values to report on.
//
// A CPU profile carries [samples/count, cpu/nanoseconds]; a heap profile
// carries four, of which inuse_space is the one people mean. pprof records its
// own preference in DefaultSampleType, so honour that and fall back to the
// last, which is the convention both profile kinds follow.
func valueIndex(p *profile.Profile) int {
	if p.DefaultSampleType != "" {
		for i, st := range p.SampleType {
			if st.Type == p.DefaultSampleType {
				return i
			}
		}
	}
	if len(p.SampleType) == 0 {
		return 0
	}
	return len(p.SampleType) - 1
}

// leafFunction returns the name of the frame a sample was actually executing.
func leafFunction(s *profile.Sample) string {
	for _, loc := range s.Location {
		for _, ln := range loc.Line {
			if ln.Function != nil {
				return ln.Function.Name
			}
		}
	}
	return ""
}

type nodeStat struct {
	name string
	flat int64
	cum  int64
}

// collect computes flat and cumulative totals per function.
//
// flat is what a function was executing itself; cum includes everything it
// called. A function appearing twice in one stack -- recursion -- counts once
// toward cum, otherwise a recursive call would multiply its own total.
func collect(p *profile.Profile, idx int) (stats []nodeStat, total int64) {
	flat := map[string]int64{}
	cum := map[string]int64{}

	for _, s := range p.Sample {
		if idx >= len(s.Value) {
			continue
		}
		v := s.Value[idx]
		total += v

		if leaf := leafFunction(s); leaf != "" {
			flat[leaf] += v
		}

		seen := map[string]bool{}
		for _, loc := range s.Location {
			for _, ln := range loc.Line {
				if ln.Function == nil || seen[ln.Function.Name] {
					continue
				}
				seen[ln.Function.Name] = true
				cum[ln.Function.Name] += v
			}
		}
	}

	for name, c := range cum {
		stats = append(stats, nodeStat{name: name, flat: flat[name], cum: c})
	}
	// Functions that only ever appear as a leaf still need a row.
	for name, fv := range flat {
		if _, ok := cum[name]; !ok {
			stats = append(stats, nodeStat{name: name, flat: fv, cum: fv})
		}
	}

	sort.Slice(stats, func(i, j int) bool {
		if stats[i].flat != stats[j].flat {
			return stats[i].flat > stats[j].flat
		}
		if stats[i].cum != stats[j].cum {
			return stats[i].cum > stats[j].cum
		}
		return stats[i].name < stats[j].name
	})
	return stats, total
}

// formatValue renders a sample value in the units the profile declares.
func formatValue(v int64, unit string) string {
	switch unit {
	case "nanoseconds":
		return time.Duration(v).String()
	case "bytes":
		return formatBytes(v)
	default:
		return fmt.Sprintf("%d", v)
	}
}

func formatBytes(v int64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%dB", v)
	}
	div, exp := int64(unit), 0
	for n := v / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f%cB", float64(v)/float64(div), "KMGTPE"[exp])
}

func percent(part, whole int64) string {
	if whole == 0 {
		return "0%"
	}
	return fmt.Sprintf("%.2f%%", float64(part)/float64(whole)*100)
}

// header reproduces the preamble `go tool pprof` prints, which operators read
// as much as the table: it says what was measured and for how long.
func header(p *profile.Profile, idx int, total int64) string {
	var b strings.Builder
	st := "unknown"
	unit := ""
	if idx < len(p.SampleType) {
		st = p.SampleType[idx].Type
		unit = p.SampleType[idx].Unit
	}
	fmt.Fprintf(&b, "Type: %s\n", st)
	if p.TimeNanos > 0 {
		fmt.Fprintf(&b, "Time: %s\n", time.Unix(0, p.TimeNanos).UTC().Format(time.RFC3339))
	}
	if p.DurationNanos > 0 {
		fmt.Fprintf(&b, "Duration: %s, Total samples = %s\n",
			time.Duration(p.DurationNanos), formatValue(total, unit))
	} else {
		fmt.Fprintf(&b, "Total samples = %s\n", formatValue(total, unit))
	}
	return b.String()
}

// emptyNote explains a profile with nothing in it.
//
// This is the common case, not an edge case: Go's CPU profiler samples at
// 100 Hz, so a call shorter than ~10ms usually captures nothing at all. A bare
// empty table invites the reader to conclude the function is free.
func emptyNote(p *profile.Profile) string {
	if p.Period > 0 && p.PeriodType != nil && p.PeriodType.Unit == "nanoseconds" {
		return fmt.Sprintf(
			"\nNo samples. The CPU profiler samples every %s, so a call shorter "+
				"than that usually finishes between two samples and records nothing. "+
				"This does not mean the function used no CPU.\n",
			time.Duration(p.Period))
	}
	return "\nNo samples were recorded in this profile.\n"
}

func renderTop(p *profile.Profile) string {
	idx := valueIndex(p)
	stats, total := collect(p, idx)

	var b strings.Builder
	b.WriteString(header(p, idx, total))
	if len(stats) == 0 || total == 0 {
		b.WriteString(emptyNote(p))
		return b.String()
	}

	unit := ""
	if idx < len(p.SampleType) {
		unit = p.SampleType[idx].Unit
	}

	shown := stats
	if len(shown) > maxReportNodes {
		shown = shown[:maxReportNodes]
	}
	var accounted int64
	for _, s := range shown {
		accounted += s.flat
	}
	fmt.Fprintf(&b, "Showing nodes accounting for %s, %s of %s total\n\n",
		formatValue(accounted, unit), percent(accounted, total), formatValue(total, unit))

	fmt.Fprintf(&b, "%10s %7s %7s %10s %7s  %s\n", "flat", "flat%", "sum%", "cum", "cum%", "function")
	var running int64
	for _, s := range shown {
		running += s.flat
		fmt.Fprintf(&b, "%10s %7s %7s %10s %7s  %s\n",
			formatValue(s.flat, unit), percent(s.flat, total), percent(running, total),
			formatValue(s.cum, unit), percent(s.cum, total), s.name)
	}
	if len(stats) > len(shown) {
		fmt.Fprintf(&b, "\n(%d more functions not shown)\n", len(stats)-len(shown))
	}
	return b.String()
}

// renderTree shows each function with the callers that reached it and the
// callees it reached, which is what `pprof -tree` is for.
func renderTree(p *profile.Profile) string {
	idx := valueIndex(p)
	stats, total := collect(p, idx)

	var b strings.Builder
	b.WriteString(header(p, idx, total))
	if len(stats) == 0 || total == 0 {
		b.WriteString(emptyNote(p))
		return b.String()
	}

	unit := ""
	if idx < len(p.SampleType) {
		unit = p.SampleType[idx].Unit
	}

	// Edge weights, keyed caller -> callee. Location order within a sample is
	// leaf-first, so the caller of frame i is frame i+1.
	callers := map[string]map[string]int64{}
	callees := map[string]map[string]int64{}
	for _, s := range p.Sample {
		if idx >= len(s.Value) {
			continue
		}
		v := s.Value[idx]
		frames := sampleFunctions(s)
		for i := 0; i+1 < len(frames); i++ {
			callee, caller := frames[i], frames[i+1]
			if callers[callee] == nil {
				callers[callee] = map[string]int64{}
			}
			if callees[caller] == nil {
				callees[caller] = map[string]int64{}
			}
			callers[callee][caller] += v
			callees[caller][callee] += v
		}
	}

	shown := stats
	if len(shown) > maxReportNodes {
		shown = shown[:maxReportNodes]
	}
	fmt.Fprintf(&b, "Showing top %d nodes out of %d\n", len(shown), len(stats))

	for _, s := range shown {
		b.WriteString("\n----------------------------------------------------------\n")
		for _, e := range sortedEdges(callers[s.name]) {
			fmt.Fprintf(&b, "%12s %7s |   %s\n", formatValue(e.weight, unit), percent(e.weight, total), e.name)
		}
		fmt.Fprintf(&b, "%12s %7s %10s %7s  %s\n",
			formatValue(s.flat, unit), percent(s.flat, total),
			formatValue(s.cum, unit), percent(s.cum, total), s.name)
		for _, e := range sortedEdges(callees[s.name]) {
			fmt.Fprintf(&b, "%12s %7s |     %s\n", formatValue(e.weight, unit), percent(e.weight, total), e.name)
		}
	}
	return b.String()
}

// renderTraces lists every distinct stack with its value.
func renderTraces(p *profile.Profile) string {
	idx := valueIndex(p)
	_, total := collect(p, idx)

	var b strings.Builder
	b.WriteString(header(p, idx, total))
	if len(p.Sample) == 0 {
		b.WriteString(emptyNote(p))
		return b.String()
	}

	unit := ""
	if idx < len(p.SampleType) {
		unit = p.SampleType[idx].Unit
	}

	for i, s := range p.Sample {
		if i >= maxReportNodes {
			fmt.Fprintf(&b, "\n(%d more samples not shown)\n", len(p.Sample)-maxReportNodes)
			break
		}
		if idx >= len(s.Value) {
			continue
		}
		b.WriteString("-----------+-------------------------------------------------------\n")
		frames := sampleFunctions(s)
		for j, fn := range frames {
			if j == 0 {
				fmt.Fprintf(&b, "%10s   %s\n", formatValue(s.Value[idx], unit), fn)
				continue
			}
			fmt.Fprintf(&b, "%10s   %s\n", "", fn)
		}
	}
	return b.String()
}

// sampleFunctions returns a sample's frames, leaf first.
func sampleFunctions(s *profile.Sample) []string {
	var out []string
	for _, loc := range s.Location {
		for _, ln := range loc.Line {
			if ln.Function != nil {
				out = append(out, ln.Function.Name)
			}
		}
	}
	return out
}

type edge struct {
	name   string
	weight int64
}

func sortedEdges(m map[string]int64) []edge {
	out := make([]edge, 0, len(m))
	for n, w := range m {
		out = append(out, edge{name: n, weight: w})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].weight != out[j].weight {
			return out[i].weight > out[j].weight
		}
		return out[i].name < out[j].name
	})
	return out
}

// renderAnnotatedSource replaces `go tool pprof -list`.
//
// -list needed two things production does not have: the Go toolchain, and the
// source tree the binary was built from. This splits those apart. The
// per-line sample counts come from the profile itself, which always carries
// them, so the useful half works in a scratch container. The source text is
// read from the path the profile recorded and interleaved only when that file
// is actually readable -- on a developer machine it usually is, in a container
// it usually is not, and saying so is better than an error that reads like a
// malfunction.
func renderAnnotatedSource(path, funcName string) string {
	if path == "" {
		return "No CPU profile was captured for this function."
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "No profile on disk. Profiles are written only for sampled calls."
		}
		return fmt.Sprintf("Could not open the profile: %v", err)
	}
	defer f.Close()

	p, err := profile.Parse(f)
	if err != nil {
		return fmt.Sprintf("Could not parse the profile: %v", err)
	}

	idx := valueIndex(p)
	unit := ""
	if idx < len(p.SampleType) {
		unit = p.SampleType[idx].Unit
	}

	// line number -> flat value, for the requested function only.
	perLine := map[int64]int64{}
	var sourceFile string
	var total int64

	for _, s := range p.Sample {
		if idx >= len(s.Value) {
			continue
		}
		v := s.Value[idx]
		// Only the leaf frame attributes flat cost to a line.
		for _, loc := range s.Location {
			hit := false
			for _, ln := range loc.Line {
				if ln.Function == nil || ln.Function.Name != funcName {
					continue
				}
				perLine[ln.Line] += v
				total += v
				if sourceFile == "" {
					sourceFile = ln.Function.Filename
				}
				hit = true
			}
			if hit {
				break
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Function: %s\n", funcName)
	if sourceFile != "" {
		fmt.Fprintf(&b, "File: %s\n", sourceFile)
	}
	fmt.Fprintf(&b, "Type: %s\n\n", sampleTypeName(p, idx))

	if len(perLine) == 0 {
		b.WriteString("No samples fell inside this function.\n")
		b.WriteString(strings.TrimPrefix(emptyNote(p), "\n"))
		return b.String()
	}

	src, srcErr := os.ReadFile(sourceFile)
	if srcErr != nil {
		fmt.Fprintf(&b, "Source is not available on this host, so only the line "+
			"numbers the profiler recorded are shown.\n\n")
		for _, ln := range sortedLines(perLine) {
			fmt.Fprintf(&b, "%12s  %s:%d\n", formatValue(perLine[ln], unit), sourceFile, ln)
		}
		return b.String()
	}

	lines := strings.Split(string(src), "\n")
	shown := sortedLines(perLine)
	first, last := shown[0], shown[len(shown)-1]
	// A little context either side, clamped to the file.
	const pad = 3
	from, to := int(first)-pad, int(last)+pad
	if from < 1 {
		from = 1
	}
	if to > len(lines) {
		to = len(lines)
	}

	for i := from; i <= to; i++ {
		v, ok := perLine[int64(i)]
		marker := "     "
		amount := ""
		if ok && v > 0 {
			marker = "  .  "
			amount = formatValue(v, unit)
		}
		fmt.Fprintf(&b, "%12s %s %5d:  %s\n", amount, marker, i, lines[i-1])
	}
	return b.String()
}

func sampleTypeName(p *profile.Profile, idx int) string {
	if idx < len(p.SampleType) {
		return p.SampleType[idx].Type
	}
	return "unknown"
}

func sortedLines(m map[int64]int64) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
