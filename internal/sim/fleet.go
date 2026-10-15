// Package sim is a fake fleet of cells serving traffic. Each cell's error
// rate and latency come from its config through a small model of a service
// calling an upstream, so a bad value does real (simulated) damage.
package sim

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/youssefm1/rollguard/internal/config"
	"github.com/youssefm1/rollguard/internal/slo"
)

// Model is how a cell turns config into errors and latency.
type Model struct {
	RPS            float64 // requests per second per cell
	BaseErrorRate  float64 // errors with a healthy config
	UpstreamMedian float64 // upstream latency median, ms (lognormal)
	UpstreamSigma  float64 // lognormal sigma
	OverheadMs     float64 // our own processing time
	LatencyNoise   float64 // relative stddev on p99 per tick
}

// DefaultModel: ~40ms median upstream, ~130ms upstream p99.
var DefaultModel = Model{
	RPS:            1000,
	BaseErrorRate:  0.001,
	UpstreamMedian: 40,
	UpstreamSigma:  0.5,
	OverheadMs:     8,
	LatencyNoise:   0.04,
}

// Effect is the steady-state behaviour of a cell under one config.
type Effect struct {
	ErrorRate float64
	P99Ms     float64
}

// Eval works out the effect of a config.
//
//   - upstream_timeout_ms: an attempt fails if the upstream is slower than
//     this. A request fails if every attempt (1 + retries) fails.
//   - cache_mb: hit ratio is 1 - e^(-cache_mb/512). Misses add load to the
//     upstream, which pushes tail latency up.
func (m Model) Eval(c config.Config) Effect {
	timeout := float64(c.Int("upstream_timeout_ms", 1000))
	retries := c.Int("retries", 1)
	cacheMB := float64(c.Int("cache_mb", 2048))

	miss := math.Exp(-cacheMB / 512)
	load := 1 + 1.2*miss // upstream slows down as more requests miss
	median := m.UpstreamMedian * load
	up99 := median * math.Exp(2.326*m.UpstreamSigma)

	pFail := 1 - lognormCDF(timeout, median, m.UpstreamSigma)
	errRate := m.BaseErrorRate + math.Pow(pFail, float64(retries+1))
	if errRate > 1 {
		errRate = 1
	}

	p99 := up99
	if pFail > 0.01 {
		// the tail is now requests burning through every retry
		p99 = timeout * float64(retries+1)
	}
	return Effect{ErrorRate: errRate, P99Ms: p99 + m.OverheadMs}
}

func lognormCDF(x, median, sigma float64) float64 {
	if x <= 0 {
		return 0
	}
	return 0.5 * math.Erfc(-(math.Log(x)-math.Log(median))/(sigma*math.Sqrt2))
}

// Tick is what one cell served during one clock step.
type Tick struct {
	Requests float64
	Errors   float64
	P99Ms    float64
}

// Cell is one simulated serving cell.
type Cell struct {
	ID     string
	Region string
	cfg    config.Config
	effect Effect
	hist   []Tick // newest last, capped
}

// Fleet is a set of cells. It implements rollout.Fleet and rollout.Signals.
type Fleet struct {
	mu       sync.Mutex
	model    Model
	cells    []*Cell
	byID     map[string]*Cell
	rng      *rand.Rand
	histLen  int
	step     time.Duration
	failNext map[string]error // injected Apply failures, for tests
}

// NewFleet makes n cells spread across regions, all on initial.
func NewFleet(n int, regions []string, initial config.Config, model Model, seed int64) *Fleet {
	if len(regions) == 0 {
		regions = []string{"us-east1"}
	}
	f := &Fleet{
		model:    model,
		byID:     make(map[string]*Cell, n),
		rng:      rand.New(rand.NewSource(seed)),
		histLen:  600,
		step:     time.Second,
		failNext: map[string]error{},
	}
	eff := model.Eval(initial)
	for i := 0; i < n; i++ {
		c := &Cell{
			ID:     fmt.Sprintf("%s-c%03d", regions[i%len(regions)], i),
			Region: regions[i%len(regions)],
			cfg:    initial,
			effect: eff,
		}
		f.cells = append(f.cells, c)
		f.byID[c.ID] = c
	}
	return f
}

// Model returns the fleet's behaviour model.
func (f *Fleet) Model() Model { return f.model }

// Cells returns every cell ID, sorted.
func (f *Fleet) Cells() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, len(f.cells))
	for i, c := range f.cells {
		ids[i] = c.ID
	}
	sort.Strings(ids)
	return ids
}

// Current returns the config a cell is running.
func (f *Fleet) Current(id string) (config.Config, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[id]
	if !ok {
		return config.Config{}, fmt.Errorf("unknown cell %q", id)
	}
	return c.cfg, nil
}

// Apply pushes a config to one cell. Takes effect on the next tick.
func (f *Fleet) Apply(_ context.Context, id string, cfg config.Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[id]
	if !ok {
		return fmt.Errorf("unknown cell %q", id)
	}
	if err, ok := f.failNext[id]; ok {
		delete(f.failNext, id)
		return err
	}
	c.cfg = cfg
	c.effect = f.model.Eval(cfg)
	return nil
}

// Effect is the modelled steady-state behaviour of a cell, without noise.
func (f *Fleet) Effect(id string) Effect {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.byID[id]; ok {
		return c.effect
	}
	return Effect{}
}

// FailNextApply makes the next Apply to id return err.
func (f *Fleet) FailNextApply(id string, err error) {
	f.mu.Lock()
	f.failNext[id] = err
	f.mu.Unlock()
}

// Step generates one tick of traffic on every cell. Hook it to the clock.
func (f *Fleet) Step(dt time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.step = dt
	for _, c := range f.cells {
		n := f.model.RPS * dt.Seconds()
		p := c.effect.ErrorRate
		// normal approximation to binomial
		errs := n*p + f.rng.NormFloat64()*math.Sqrt(n*p*(1-p))
		if errs < 0 {
			errs = 0
		}
		p99 := c.effect.P99Ms * (1 + f.rng.NormFloat64()*f.model.LatencyNoise)
		c.hist = append(c.hist, Tick{Requests: n, Errors: math.Round(errs), P99Ms: p99})
		if len(c.hist) > f.histLen {
			c.hist = c.hist[len(c.hist)-f.histLen:]
		}
	}
}

// Sample aggregates the last window of traffic across ids.
func (f *Fleet) Sample(ids []string, window time.Duration) slo.Sample {
	f.mu.Lock()
	defer f.mu.Unlock()
	ticks := int(math.Ceil(float64(window) / float64(f.step)))
	if ticks < 1 {
		ticks = 1
	}
	var s slo.Sample
	var p99w float64
	for _, id := range ids {
		c, ok := f.byID[id]
		if !ok {
			continue
		}
		s.Cells++
		h := c.hist
		if len(h) > ticks {
			h = h[len(h)-ticks:]
		}
		for _, t := range h {
			s.Requests += t.Requests
			s.Errors += t.Errors
			p99w += t.P99Ms * t.Requests
		}
	}
	if s.Requests > 0 {
		s.P99 = time.Duration(p99w / s.Requests * float64(time.Millisecond))
	}
	return s
}
