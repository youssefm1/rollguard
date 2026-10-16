package rollout

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/youssefm1/rollguard/internal/config"
	"github.com/youssefm1/rollguard/internal/slo"
)

// Fleet is where configs get pushed.
type Fleet interface {
	Cells() []string
	Current(cell string) (config.Config, error)
	Apply(ctx context.Context, cell string, cfg config.Config) error
}

// Signals reads SLO metrics for a group of cells over the last window.
type Signals interface {
	Sample(cells []string, window time.Duration) slo.Sample
}

// Clock lets the demo and tests run on fake time.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

// RealClock is the wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time        { return time.Now() }
func (RealClock) Sleep(d time.Duration) { time.Sleep(d) }

// Options controls pacing and what counts as a bad wave.
type Options struct {
	Waves           []float64
	Bake            time.Duration // how long to watch each wave
	Interval        time.Duration // time between SLO samples
	Objective       slo.Objective
	GateWindow      int // trip on GateMaxBad bad samples out of GateWindow
	GateMaxBad      int
	RecoveryTimeout time.Duration // how long to wait for rolled-back cells to look healthy
	ChangeID        string        // salts cell selection; defaults to the config version
	OnEvent         func(Event)
}

// DefaultOptions is 1/10/50/100 with a 60s bake and 5s samples.
func DefaultOptions() Options {
	return Options{
		Waves:           DefaultWaves,
		Bake:            60 * time.Second,
		Interval:        5 * time.Second,
		Objective:       slo.DefaultObjective,
		GateWindow:      4,
		GateMaxBad:      2,
		RecoveryTimeout: 60 * time.Second,
	}
}

// Outcome is how a rollout ended.
type Outcome string

const (
	Completed  Outcome = "completed"
	RolledBack Outcome = "rolled_back"
	Aborted    Outcome = "aborted"
)

// Event is one line of the rollout's audit log.
type Event struct {
	At         time.Time     `json:"at"`
	Kind       string        `json:"kind"`
	Wave       int           `json:"wave,omitempty"`
	Percent    float64       `json:"percent,omitempty"`
	Cells      int           `json:"cells,omitempty"`
	CanaryErr  float64       `json:"canary_err,omitempty"`
	ControlErr float64       `json:"control_err,omitempty"`
	CanaryP99  time.Duration `json:"canary_p99_ns,omitempty"`
	ControlP99 time.Duration `json:"control_p99_ns,omitempty"`
	Detail     string        `json:"detail,omitempty"`
}

// Result is what happened.
type Result struct {
	Outcome      Outcome
	Version      string
	Plan         Plan
	FailedWave   int // 0 if none
	Reason       string
	ExposedCells int // most cells that ever ran the new config at once
	FirstPush    time.Time
	DetectedAt   time.Time // zero unless a breach tripped the gate
	RolledBackAt time.Time
	Recovered    bool
	Events       []Event
}

// TimeToDetect is first push to breach.
func (r Result) TimeToDetect() time.Duration {
	if r.DetectedAt.IsZero() {
		return 0
	}
	return r.DetectedAt.Sub(r.FirstPush)
}

// Engine runs rollouts.
type Engine struct {
	fleet   Fleet
	signals Signals
	clock   Clock
	opts    Options
}

// New builds an engine. Zero fields in opts fall back to DefaultOptions.
func New(f Fleet, s Signals, c Clock, opts Options) *Engine {
	d := DefaultOptions()
	if len(opts.Waves) == 0 {
		opts.Waves = d.Waves
	}
	if opts.Bake <= 0 {
		opts.Bake = d.Bake
	}
	if opts.Interval <= 0 {
		opts.Interval = d.Interval
	}
	if opts.Objective == (slo.Objective{}) {
		opts.Objective = d.Objective
	}
	if opts.GateWindow <= 0 {
		opts.GateWindow = d.GateWindow
	}
	if opts.GateMaxBad <= 0 {
		opts.GateMaxBad = d.GateMaxBad
	}
	if opts.RecoveryTimeout <= 0 {
		opts.RecoveryTimeout = d.RecoveryTimeout
	}
	if c == nil {
		c = RealClock{}
	}
	return &Engine{fleet: f, signals: s, clock: c, opts: opts}
}

// Run rolls cfg out wave by wave. It only returns an error for problems
// that stop it from even starting or from rolling back; a bad wave is a
// normal RolledBack result.
func (e *Engine) Run(ctx context.Context, cfg config.Config) (Result, error) {
	res := Result{Version: cfg.Version()}
	emit := func(ev Event) {
		ev.At = e.clock.Now()
		res.Events = append(res.Events, ev)
		if e.opts.OnEvent != nil {
			e.opts.OnEvent(ev)
		}
	}

	cells := e.fleet.Cells()
	prev := make(map[string]config.Config, len(cells))
	for _, c := range cells {
		cur, err := e.fleet.Current(c)
		if err != nil {
			return res, fmt.Errorf("snapshot %s: %w", c, err)
		}
		prev[c] = cur
	}

	salt := e.opts.ChangeID
	if salt == "" {
		salt = res.Version
	}
	plan, err := NewPlan(cells, e.opts.Waves, salt)
	if err != nil {
		return res, err
	}
	res.Plan = plan
	emit(Event{Kind: "plan", Cells: len(cells), Detail: fmt.Sprintf("version %s, %d waves", res.Version, len(plan.Waves))})

	onNew := map[string]bool{}
	var updated []string
	fail := func(wave int, out Outcome, reason string) (Result, error) {
		res.Outcome = out
		res.FailedWave = wave
		res.Reason = reason
		err := e.rollback(ctx, updated, prev, &res, emit)
		return res, err
	}

	for _, w := range plan.Waves {
		if ctx.Err() != nil {
			return fail(w.Index, Aborted, ctx.Err().Error())
		}
		emit(Event{Kind: "push", Wave: w.Index, Percent: w.Percent, Cells: len(w.Cells), Detail: preview(w.Cells)})
		if res.FirstPush.IsZero() {
			res.FirstPush = e.clock.Now()
		}
		for _, c := range w.Cells {
			if err := e.fleet.Apply(ctx, c, cfg); err != nil {
				emit(Event{Kind: "apply_error", Wave: w.Index, Detail: fmt.Sprintf("%s: %v", c, err)})
				return fail(w.Index, RolledBack, fmt.Sprintf("push to %s failed: %v", c, err))
			}
			onNew[c] = true
			updated = append(updated, c)
		}
		res.ExposedCells = len(updated)
		control := others(cells, onNew)

		gate := slo.NewGate(e.opts.GateWindow, e.opts.GateMaxBad)
		for waited := time.Duration(0); waited < e.opts.Bake; waited += e.opts.Interval {
			if ctx.Err() != nil {
				return fail(w.Index, Aborted, ctx.Err().Error())
			}
			e.clock.Sleep(e.opts.Interval)
			can := e.signals.Sample(updated, e.opts.Interval)
			ctl := e.signals.Sample(control, e.opts.Interval)
			v := e.opts.Objective.Compare(can, ctl)
			emit(Event{
				Kind: "sample", Wave: w.Index, Cells: can.Cells,
				CanaryErr: can.ErrorRate(), ControlErr: ctl.ErrorRate(),
				CanaryP99: can.P99, ControlP99: ctl.P99,
				Detail: v.String(),
			})
			if gate.Observe(v) {
				res.DetectedAt = e.clock.Now()
				reason := strings.Join(v.Reasons, "; ")
				emit(Event{Kind: "breach", Wave: w.Index, Cells: len(updated),
					Detail: fmt.Sprintf("%d/%d bad samples: %s", gate.Bad(), gate.Window, reason)})
				return fail(w.Index, RolledBack, reason)
			}
		}
		emit(Event{Kind: "wave_ok", Wave: w.Index, Percent: w.Percent, Cells: len(updated)})
	}

	res.Outcome = Completed
	emit(Event{Kind: "complete", Cells: len(updated), Detail: "version " + res.Version + " on every cell"})
	return res, nil
}

// rollback puts every updated cell back on the config it had before the
// rollout, all at once (no waves on the way back), then watches until they
// look healthy again.
func (e *Engine) rollback(ctx context.Context, updated []string, prev map[string]config.Config, res *Result, emit func(Event)) error {
	if len(updated) == 0 {
		return nil
	}
	emit(Event{Kind: "rollback", Cells: len(updated), Detail: "reverting " + preview(updated)})
	res.RolledBackAt = e.clock.Now()

	// Rollback has to finish even if the caller's context is gone.
	rctx := context.WithoutCancel(ctx)
	var errs []error
	for i := len(updated) - 1; i >= 0; i-- {
		c := updated[i]
		err := e.fleet.Apply(rctx, c, prev[c])
		if err != nil {
			err = e.fleet.Apply(rctx, c, prev[c]) // one retry
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c, err))
		}
	}
	if len(errs) > 0 {
		emit(Event{Kind: "rollback_error", Detail: errors.Join(errs...).Error()})
		return fmt.Errorf("rollback incomplete: %w", errors.Join(errs...))
	}

	reverted := map[string]bool{}
	for _, c := range updated {
		reverted[c] = true
	}
	control := others(e.fleet.Cells(), reverted)
	good := 0
	for waited := time.Duration(0); waited < e.opts.RecoveryTimeout; waited += e.opts.Interval {
		e.clock.Sleep(e.opts.Interval)
		can := e.signals.Sample(updated, e.opts.Interval)
		ctl := e.signals.Sample(control, e.opts.Interval)
		v := e.opts.Objective.Compare(can, ctl)
		emit(Event{Kind: "sample", Cells: can.Cells, CanaryErr: can.ErrorRate(), ControlErr: ctl.ErrorRate(),
			CanaryP99: can.P99, ControlP99: ctl.P99, Detail: "post-rollback " + v.String()})
		if v.Healthy && !v.Skipped {
			good++
		} else {
			good = 0
		}
		if good >= 2 {
			res.Recovered = true
			emit(Event{Kind: "recovered", Cells: len(updated)})
			return nil
		}
	}
	emit(Event{Kind: "not_recovered", Cells: len(updated), Detail: "rolled-back cells still unhealthy, page a human"})
	return nil
}

func others(all []string, exclude map[string]bool) []string {
	out := make([]string, 0, len(all))
	for _, c := range all {
		if !exclude[c] {
			out = append(out, c)
		}
	}
	return out
}

func preview(cells []string) string {
	const max = 4
	if len(cells) <= max {
		return strings.Join(cells, ",")
	}
	return fmt.Sprintf("%s,... (+%d)", strings.Join(cells[:max], ","), len(cells)-max)
}
