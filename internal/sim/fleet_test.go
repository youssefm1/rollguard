package sim

import (
	"context"
	"testing"
	"time"

	"github.com/youssefm1/rollguard/internal/config"
)

var base = config.Config{Values: map[string]any{"upstream_timeout_ms": 1000, "retries": 1, "cache_mb": 2048}}

func TestModel(t *testing.T) {
	m := DefaultModel
	healthy := m.Eval(base)
	if healthy.ErrorRate > 0.002 || healthy.P99Ms > 200 {
		t.Fatalf("baseline not healthy: %+v", healthy)
	}
	if e := m.Eval(base.With("upstream_timeout_ms", 50)); e.ErrorRate < 0.05 {
		t.Errorf("50ms timeout should fail a lot, got %+v", e)
	}
	if e := m.Eval(base.With("upstream_timeout_ms", 50).With("retries", 3)); e.ErrorRate >= m.Eval(base.With("upstream_timeout_ms", 50)).ErrorRate {
		t.Errorf("more retries should lower errors, got %+v", e)
	}
	if e := m.Eval(base.With("cache_mb", 0)); e.P99Ms < 1.5*healthy.P99Ms || e.ErrorRate > 0.002 {
		t.Errorf("no cache should hurt latency only, got %+v", e)
	}
}

func TestFleetSampleAndApply(t *testing.T) {
	f := NewFleet(10, []string{"r1", "r2"}, base, DefaultModel, 7)
	clock := NewClock(time.Second)
	clock.OnTick(func(time.Time) { f.Step(clock.Step()) })
	clock.Sleep(5 * time.Second)

	ids := f.Cells()
	s := f.Sample(ids[:2], 5*time.Second)
	if s.Cells != 2 || s.Requests != 2*5*DefaultModel.RPS {
		t.Fatalf("sample = %+v", s)
	}
	if err := f.Apply(context.Background(), ids[0], base.With("upstream_timeout_ms", 50)); err != nil {
		t.Fatal(err)
	}
	clock.Sleep(5 * time.Second)
	if bad, good := f.Sample(ids[:1], 5*time.Second), f.Sample(ids[1:], 5*time.Second); bad.ErrorRate() < 10*good.ErrorRate() {
		t.Fatalf("bad cell %.4f vs good %.4f", bad.ErrorRate(), good.ErrorRate())
	}
	if err := f.Apply(context.Background(), "nope", base); err == nil {
		t.Fatal("unknown cell accepted")
	}
}
