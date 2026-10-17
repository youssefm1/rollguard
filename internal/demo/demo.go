// Package demo runs chaos scenarios against the simulated fleet: the same
// config goes out once in waves and once to every cell at once, under the
// same SLO watcher, so the only difference is the wave plan.
package demo

import (
	"context"
	"fmt"
	"time"

	"github.com/youssefm1/rollguard/internal/config"
	"github.com/youssefm1/rollguard/internal/rollout"
	"github.com/youssefm1/rollguard/internal/sim"
)

// Scenario is a config change to try.
type Scenario struct {
	Name   string
	Story  string
	Signal string // "errors" or "latency": what the graph leads with
	Apply  func(config.Config) config.Config
}

// Baseline is the config every cell starts on.
var Baseline = config.Config{
	Name: "frontend-proxy",
	Values: map[string]any{
		"upstream_timeout_ms": 1000,
		"retries":             1,
		"cache_mb":            2048,
		"log_level":           "info",
	},
}

// Scenarios by name.
var Scenarios = map[string]Scenario{
	"timeout": {
		Name:   "timeout",
		Signal: "errors",
		Story:  "upstream_timeout_ms 1000 -> 50 (meant 500). Passes validation, times out most upstream calls.",
		Apply:  func(c config.Config) config.Config { return c.With("upstream_timeout_ms", 50) },
	},
	"latency": {
		Name:   "latency",
		Signal: "latency",
		Story:  "cache_mb 2048 -> 0 (someone read 0 as unlimited). No errors, but every request misses and p99 doubles.",
		Apply:  func(c config.Config) config.Config { return c.With("cache_mb", 0) },
	},
	"safe": {
		Name:   "safe",
		Signal: "errors",
		Story:  "log_level info -> warn. Harmless; should go all the way to 100%.",
		Apply:  func(c config.Config) config.Config { return c.With("log_level", "warn") },
	},
}

// Point is one tick of fleet-wide numbers.
type Point struct {
	T          float64 // seconds since start
	FleetErr   float64 // error rate across every cell
	CanaryErr  float64 // error rate on cells running the new version, 0 if none
	ControlErr float64
	FleetP99   float64 // ms
	CanaryP99  float64 // ms, 0 if no cells on the new version
	ControlP99 float64 // ms
	OnNew      float64 // share of cells on the new version
	ExcessErrs float64 // cumulative expected errors above baseline caused by the new version
}

// Mark is a rollout event at a point in time, for the graph.
type Mark struct {
	T     float64
	Kind  string
	Label string
}

// Run is one rollout of a scenario.
type Run struct {
	Label  string
	Result rollout.Result
	Series []Point
	Cells  int
	Marks  []Mark
	// seconds since start
	PushAt, DetectAt, RollbackAt float64
}

// PeakFleetErr is the worst fleet-wide error rate seen.
func (r Run) PeakFleetErr() float64 {
	m := 0.0
	for _, p := range r.Series {
		if p.FleetErr > m {
			m = p.FleetErr
		}
	}
	return m
}

// ExcessErrors is failed requests caused by the change.
func (r Run) ExcessErrors() float64 {
	if len(r.Series) == 0 {
		return 0
	}
	if e := r.Series[len(r.Series)-1].ExcessErrs; e > 0 {
		return e
	}
	return 0
}

// Params for a demo run.
type Params struct {
	Cells   int
	Regions []string
	Seed    int64
	Lead    time.Duration // baseline traffic before the first push
	Tail    time.Duration // traffic after the rollout ends
	Opts    rollout.Options
	OnEvent func(label string, ev rollout.Event, elapsed time.Duration)
}

// DefaultParams: 200 cells over 4 regions, so 1% is 2 cells.
func DefaultParams() Params {
	return Params{
		Cells:   200,
		Regions: []string{"us-east1", "us-central1", "europe-west1", "asia-east1"},
		Seed:    1,
		Lead:    20 * time.Second,
		Tail:    30 * time.Second,
		Opts:    rollout.DefaultOptions(),
	}
}

// Execute runs sc once with p.Opts.Waves.
func Execute(sc Scenario, p Params, label string) (Run, error) {
	clock := sim.NewClock(time.Second)
	fleet := sim.NewFleet(p.Cells, p.Regions, Baseline, sim.DefaultModel, p.Seed)
	next := sc.Apply(Baseline)
	if err := config.DefaultSchema.Validate(next); err != nil {
		return Run{}, err
	}
	newVer := next.Version()
	base := sim.DefaultModel.BaseErrorRate

	run := Run{Label: label, Cells: p.Cells}
	var excess float64
	clock.OnTick(func(time.Time) {
		fleet.Step(clock.Step())
		var onNew, onOld []string
		for _, id := range fleet.Cells() {
			c, _ := fleet.Current(id)
			if c.Version() == newVer {
				onNew = append(onNew, id)
			} else {
				onOld = append(onOld, id)
			}
		}
		all := fleet.Sample(fleet.Cells(), clock.Step())
		can := fleet.Sample(onNew, clock.Step())
		ctl := fleet.Sample(onOld, clock.Step())
		// Use the model, not the noisy samples, so a harmless change
		// scores zero instead of whatever the dice said.
		for _, id := range onNew {
			excess += (fleet.Effect(id).ErrorRate - base) * sim.DefaultModel.RPS * clock.Step().Seconds()
		}
		run.Series = append(run.Series, Point{
			T:          clock.Elapsed().Seconds(),
			FleetErr:   all.ErrorRate(),
			CanaryErr:  can.ErrorRate(),
			ControlErr: ctl.ErrorRate(),
			FleetP99:   ms(all.P99),
			CanaryP99:  ms(can.P99),
			ControlP99: ms(ctl.P99),
			OnNew:      float64(len(onNew)) / float64(p.Cells),
			ExcessErrs: excess,
		})
	})

	opts := p.Opts
	opts.ChangeID = sc.Name
	opts.OnEvent = func(ev rollout.Event) {
		t := clock.Offset(ev.At).Seconds()
		switch ev.Kind {
		case "push":
			run.Marks = append(run.Marks, Mark{T: t, Kind: ev.Kind, Label: fmt.Sprintf("wave %d: %g%%", ev.Wave, ev.Percent)})
		case "breach":
			run.Marks = append(run.Marks, Mark{T: t, Kind: ev.Kind, Label: "SLO breach -> rollback"})
		case "recovered":
			run.Marks = append(run.Marks, Mark{T: t, Kind: ev.Kind, Label: "recovered"})
		}
		if p.OnEvent != nil {
			p.OnEvent(label, ev, clock.Elapsed())
		}
	}
	eng := rollout.New(fleet, fleet, clock, opts)

	clock.Sleep(p.Lead)
	res, err := eng.Run(context.Background(), next)
	if err != nil {
		return run, fmt.Errorf("%s: %w", label, err)
	}
	clock.Sleep(p.Tail)

	run.Result = res
	since := func(t time.Time) float64 {
		if t.IsZero() {
			return 0
		}
		return clock.Offset(t).Seconds()
	}
	run.PushAt = since(res.FirstPush)
	run.DetectAt = since(res.DetectedAt)
	run.RollbackAt = since(res.RolledBackAt)
	return run, nil
}

// Compare runs sc progressively and as a single 100% push.
func Compare(sc Scenario, p Params) (progressive, bigBang Run, err error) {
	progressive, err = Execute(sc, p, "progressive")
	if err != nil {
		return
	}
	bb := p
	bb.Opts.Waves = []float64{100}
	bigBang, err = Execute(sc, bb, "all at once")
	return
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
