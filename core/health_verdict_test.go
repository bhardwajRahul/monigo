package core

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/iyashjayesh/monigo/models"
)

// TestTheVerdictAgreesWithItsOwnExplanation is the regression test for the
// card that read "Degraded" beside the sentence "System usage is within
// limits".
//
// Healthy and IconMsg used to come from two unrelated calculations: IconMsg
// from a threshold comparison, Healthy from a composite score crossing 50.
// Nothing kept them in step, so the badge could contradict the text printed
// directly underneath it, with every number in the card inside its limit.
func TestTheVerdictAgreesWithItsOwnExplanation(t *testing.T) {
	for _, stats := range []*models.ServiceStats{healthyStats(), breachStats()} {
		health := GetServiceHealth(stats)

		for name, h := range map[string]models.Health{
			"service": health.ServiceHealth,
			"system":  health.SystemHealth,
		} {
			if h.IconMsg == "" {
				continue // an error path, which reports Healthy=false and no message
			}
			saysWithin := strings.Contains(h.IconMsg, "within limits")
			saysExceeds := strings.Contains(h.IconMsg, "exceeds allowed limits")
			if !saysWithin && !saysExceeds {
				t.Fatalf("%s: IconMsg says neither within nor exceeds: %q", name, h.IconMsg)
			}
			if saysWithin && !h.Healthy {
				t.Errorf("%s reports Healthy=false while its own message says the "+
					"usage is within limits.\n  message: %s", name, h.IconMsg)
			}
			if saysExceeds && h.Healthy {
				t.Errorf("%s reports Healthy=true while its own message says the "+
					"usage exceeds the limits.\n  message: %s", name, h.IconMsg)
			}
		}
	}
}

// TestTheVerdictIgnoresTheCompositeScore pins the separation directly.
//
// Percent is a graded score and drifts continuously; a binary verdict taken
// from it flapped as it wandered across 50 -- measured crossing twice in 45
// seconds on an idle machine, while the bars beside it did not move. Percent
// still drives the ring and the graded Message; it must not decide Healthy.
func TestTheVerdictIgnoresTheCompositeScore(t *testing.T) {
	health := GetServiceHealth(healthyStats())

	for name, h := range map[string]models.Health{
		"service": health.ServiceHealth,
		"system":  health.SystemHealth,
	} {
		if !strings.Contains(h.IconMsg, "within limits") {
			continue
		}
		// A healthy fixture kept inside its limits must read healthy no matter
		// where the composite score happens to land.
		if !h.Healthy {
			t.Errorf("%s: within limits but Healthy=false (Percent=%.2f)", name, h.Percent)
		}
		if h.Percent <= 50 && !h.Healthy {
			t.Errorf("%s: the verdict is still tracking Percent (%.2f)", name, h.Percent)
		}
	}
}

// TestBreachedFlagsTravelWithTheMessage guards the layer underneath: the flag
// and the sentence are set at the same point, so they cannot drift apart.
func TestBreachedFlagsTravelWithTheMessage(t *testing.T) {
	for label, stats := range map[string]*models.ServiceStats{
		"healthy": healthyStats(),
		"breach":  breachStats(),
	} {
		got, err := CalculateHealthScore(stats)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		for name, f := range map[string]models.HealthFields{
			"service": got.ServiceHealth,
			"system":  got.SystemHealth,
		} {
			within := strings.Contains(f.Message, "within limits")
			if within == f.Breached {
				t.Errorf("%s/%s: Breached=%v but message says %q",
					label, name, f.Breached, f.Message)
			}
		}
	}
}

// TestOneBlownResourceIsABreach guards the rule that a single saturated
// resource counts, even when the others are idle.
//
// The breach test used to run on the average of the ratios, so memory at 104%
// of its allowance beside CPU at 12% averaged to 58 and reported "within
// limits" -- the dashboard said everything was fine while a limit was being
// exceeded. Averaging is right for a graded score and wrong for a verdict.
func TestOneBlownResourceIsABreach(t *testing.T) {
	stats := breachStats()

	got, err := CalculateHealthScore(stats)
	if err != nil {
		t.Fatalf("CalculateHealthScore: %v", err)
	}

	for name, f := range map[string]models.HealthFields{
		"service": got.ServiceHealth,
		"system":  got.SystemHealth,
	} {
		if !f.Breached {
			t.Errorf("%s: a fixture that exceeds a limit reports Breached=false\n"+
				"  message: %s", name, f.Message)
		}
		if strings.Contains(f.Message, "within limits") {
			t.Errorf("%s: message claims the usage is within limits while a "+
				"resource is over it\n  message: %s", name, f.Message)
		}
	}

	// And the verdict the dashboard renders must follow.
	health := GetServiceHealth(stats)
	if health.SystemHealth.Healthy || health.ServiceHealth.Healthy {
		t.Errorf("a breaching fixture still reports Healthy "+
			"(service=%v system=%v)", health.ServiceHealth.Healthy, health.SystemHealth.Healthy)
	}
}

// TestServiceCPUDoesNotScaleWithCoreCount pins the units.
//
// Service CPU is percent of one core, the convention gopsutil and `top` use
// and the one the dashboard already shows under SERVICE CPU LOAD. It used to
// be divided by the core count and multiplied by 100, which inflated it by
// 100/TotalCores -- so the same machine reported 1.68% in load_statistics and
// 17.66% in the health message, and the error grew as machines got smaller.
//
// Core count is the only thing that varies here, so if the reading still
// scales with it, the two runs disagree.
func TestServiceCPUDoesNotScaleWithCoreCount(t *testing.T) {
	cpuFor := func(cores float64) float64 {
		stats := healthyStats()
		stats.CPUStatistics.TotalCores = cores
		_, msg, _, err := calculateServiceHealth(stats)
		if err != nil {
			t.Fatalf("cores=%.0f: %v", cores, err)
		}
		m := regexp.MustCompile(`CPU Usage ([\d.]+)%`).FindStringSubmatch(msg)
		if m == nil {
			t.Fatalf("cores=%.0f: no CPU figure in %q", cores, msg)
		}
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	// Both readings sample live process CPU, so they will not be bit-identical.
	// A core-count dependency would differ by 8x here, far outside that noise.
	four, sixtyFour := cpuFor(4), cpuFor(64)
	if four > 5 && sixtyFour > 5 {
		ratio := four / sixtyFour
		if ratio < 0.5 || ratio > 2 {
			t.Errorf("service CPU still scales with core count: "+
				"%.2f%% at 4 cores vs %.2f%% at 64 (ratio %.2f)", four, sixtyFour, ratio)
		}
	}

	// The decisive check: with a idle-ish test process and a 95% limit, the
	// reading must stay well under the limit whatever the core count. Under
	// the old arithmetic a 4-core host multiplied it by 25.
	for _, cores := range []float64{1, 2, 4, 8, 64} {
		if v := cpuFor(cores); v > 100 {
			t.Errorf("cores=%.0f: service CPU reads %.2f%%, over the whole "+
				"allowance, for a test process doing nothing", cores, v)
		}
	}
}
