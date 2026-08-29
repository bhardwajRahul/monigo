package core

import (
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
