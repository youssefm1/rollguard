package rollout_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/youssefm1/rollguard/internal/config"
	"github.com/youssefm1/rollguard/internal/rollout"
	"github.com/youssefm1/rollguard/internal/sim"
)

var base = config.Config{Name: "svc", Values: map[string]any{"upstream_timeout_ms": 1000, "retries": 1, "cache_mb": 2048}}

func setup(t *testing.T, n int, seed int64) (*sim.Fleet, *sim.Clock, rollout.Options) {
	t.Helper()
	f := sim.NewFleet(n, []string{"a", "b"}, base, sim.DefaultModel, seed)
	c := sim.NewClock(time.Second)
	c.OnTick(func(time.Time) { f.Step(c.Step()) })
	c.Sleep(10 * time.Second)
	opts := rollout.DefaultOptions()
	opts.Bake = 30 * time.Second
	return f, c, opts
}

func versions(t *testing.T, f *sim.Fleet) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, id := range f.Cells() {
		c, err := f.Current(id)
		if err != nil {
			t.Fatal(err)
		}
		out[c.Version()]++
	}
	return out
}

func TestGoodChangeCompletes(t *testing.T) {
	f, c, opts := setup(t, 100, 1)
	next := base.With("log_level", "warn")
	res, err := rollout.New(f, f, c, opts).Run(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != rollout.Completed {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Reason)
	}
	if v := versions(t, f); v[next.Version()] != 100 {
		t.Fatalf("versions after rollout: %v", v)
	}
}

func TestBadChangeCaughtInFirstWave(t *testing.T) {
	f, c, opts := setup(t, 200, 1)
	res, err := rollout.New(f, f, c, opts).Run(context.Background(), base.With("upstream_timeout_ms", 50))
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != rollout.RolledBack || res.FailedWave != 1 {
		t.Fatalf("outcome %s wave %d", res.Outcome, res.FailedWave)
	}
	if res.ExposedCells != 2 {
		t.Errorf("exposed %d cells, want 2", res.ExposedCells)
	}
	if !res.Recovered {
		t.Error("not marked recovered")
	}
	if d := res.TimeToDetect(); d <= 0 || d > 20*time.Second {
		t.Errorf("time to detect %v", d)
	}
	if v := versions(t, f); v[base.Version()] != 200 {
		t.Fatalf("not fully rolled back: %v", v)
	}
}

func TestApplyFailureRollsBack(t *testing.T) {
	f, c, opts := setup(t, 100, 1)
	next := base.With("log_level", "warn")
	plan, _ := rollout.NewPlan(f.Cells(), opts.Waves, next.Version())
	// fail somewhere in wave 2, after wave 1 already went out
	f.FailNextApply(plan.Waves[1].Cells[3], errors.New("cell unreachable"))
	res, err := rollout.New(f, f, c, opts).Run(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != rollout.RolledBack || res.FailedWave != 2 {
		t.Fatalf("outcome %s wave %d", res.Outcome, res.FailedWave)
	}
	if v := versions(t, f); v[base.Version()] != 100 {
		t.Fatalf("not fully rolled back: %v", v)
	}
}

func TestCancelRollsBack(t *testing.T) {
	f, c, opts := setup(t, 100, 1)
	ctx, cancel := context.WithCancel(context.Background())
	opts.OnEvent = func(ev rollout.Event) {
		if ev.Kind == "wave_ok" && ev.Wave == 2 {
			cancel()
		}
	}
	res, err := rollout.New(f, f, c, opts).Run(ctx, base.With("log_level", "warn"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != rollout.Aborted {
		t.Fatalf("outcome %s", res.Outcome)
	}
	if v := versions(t, f); v[base.Version()] != 100 {
		t.Fatalf("not rolled back after cancel: %v", v)
	}
}

// Noise shouldn't roll back a harmless change.
func TestNoFalseRollbacks(t *testing.T) {
	for seed := int64(1); seed <= 25; seed++ {
		f, c, opts := setup(t, 200, seed)
		res, err := rollout.New(f, f, c, opts).Run(context.Background(), base.With("log_level", "warn"))
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != rollout.Completed {
			t.Fatalf("seed %d: false rollback in wave %d: %s", seed, res.FailedWave, res.Reason)
		}
	}
}
