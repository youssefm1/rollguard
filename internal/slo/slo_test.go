package slo

import (
	"testing"
	"time"
)

func sample(cells int, reqs, errs float64, p99 time.Duration) Sample {
	return Sample{Cells: cells, Requests: reqs, Errors: errs, P99: p99}
}

func TestCompare(t *testing.T) {
	o := DefaultObjective
	ctl := sample(100, 100000, 100, 140*time.Millisecond)
	cases := []struct {
		name    string
		can     Sample
		ctl     Sample
		healthy bool
		skipped bool
	}{
		{"healthy", sample(2, 10000, 10, 140*time.Millisecond), ctl, true, false},
		{"error spike", sample(2, 10000, 1200, 110*time.Millisecond), ctl, false, false},
		{"latency spike", sample(2, 10000, 10, 300*time.Millisecond), ctl, false, false},
		{"too little traffic", sample(1, 100, 50, 0), ctl, true, true},
		// Everyone is failing, not just us: don't blame the change.
		{"fleet-wide outage", sample(2, 10000, 500, 140*time.Millisecond), sample(100, 100000, 4000, 140*time.Millisecond), true, false},
		{"no control, over slo", sample(200, 100000, 3000, 140*time.Millisecond), Sample{}, false, false},
		{"no control, under slo", sample(200, 100000, 100, 140*time.Millisecond), Sample{}, true, false},
	}
	for _, c := range cases {
		v := o.Compare(c.can, c.ctl)
		if v.Healthy != c.healthy || v.Skipped != c.skipped {
			t.Errorf("%s: got %+v", c.name, v)
		}
		if !v.Healthy && len(v.Reasons) == 0 {
			t.Errorf("%s: unhealthy with no reason", c.name)
		}
	}
}

func TestGate(t *testing.T) {
	ok, bad, skip := Verdict{Healthy: true}, Verdict{}, Verdict{Healthy: true, Skipped: true}
	g := NewGate(4, 2)
	for i, step := range []struct {
		v    Verdict
		trip bool
	}{
		{bad, false}, {ok, false}, {skip, false}, {ok, false}, {ok, false}, // first bad ages out
		{bad, false}, {bad, true},
	} {
		if got := g.Observe(step.v); got != step.trip {
			t.Fatalf("step %d: tripped=%v want %v (bad=%d)", i, got, step.trip, g.Bad())
		}
	}
	g.Reset()
	if g.Bad() != 0 {
		t.Fatal("reset didn't clear")
	}
}
