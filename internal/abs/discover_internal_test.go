package abs

import (
	"testing"
	"time"
)

func sum(ds []time.Duration) time.Duration {
	var t time.Duration
	for _, d := range ds {
		t += d
	}
	return t
}

func TestBuildBackoffs_SumsToBudget(t *testing.T) {
	t.Parallel()
	cases := []time.Duration{
		1 * time.Second,
		time.Second + 500*time.Millisecond, // budget below the first delay → single trimmed step
		90 * time.Second,
		5 * time.Minute,
		17 * time.Minute,
	}
	for _, total := range cases {
		got := buildBackoffs(total)
		if len(got) == 0 {
			t.Errorf("buildBackoffs(%s) returned no steps", total)
			continue
		}
		if s := sum(got); s != total {
			t.Errorf("buildBackoffs(%s) sums to %s, want exact budget", total, s)
		}
		for i, d := range got {
			if d > 30*time.Second {
				t.Errorf("buildBackoffs(%s) step %d = %s exceeds 30s cap", total, i, d)
			}
		}
	}
}

func TestBuildBackoffs_ZeroUsesDefault(t *testing.T) {
	t.Parallel()
	got := buildBackoffs(0)
	if s := sum(got); s != DefaultDiscoverTimeout {
		t.Errorf("buildBackoffs(0) sums to %s, want DefaultDiscoverTimeout %s", s, DefaultDiscoverTimeout)
	}
}

func TestBuildBackoffs_GrowsExponentiallyThenCaps(t *testing.T) {
	t.Parallel()
	got := buildBackoffs(5 * time.Minute)
	want := []time.Duration{
		1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("step %d = %s, want %s", i, got[i], w)
		}
	}
	// After the ramp every remaining step is the 30s cap (except the final
	// trimmed remainder).
	for i := len(want); i < len(got)-1; i++ {
		if got[i] != 30*time.Second {
			t.Errorf("step %d = %s, want 30s cap", i, got[i])
		}
	}
}
