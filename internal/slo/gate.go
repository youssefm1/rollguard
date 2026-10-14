package slo

// Gate trips when at least MaxBad of the last Window verdicts were bad.
// One noisy sample shouldn't roll back a change, but a few in a row should.
type Gate struct {
	Window int
	MaxBad int
	hist   []bool
}

// NewGate returns a gate that trips on maxBad bad verdicts out of window.
func NewGate(window, maxBad int) *Gate {
	if window < 1 {
		window = 1
	}
	if maxBad < 1 {
		maxBad = 1
	}
	if maxBad > window {
		maxBad = window
	}
	return &Gate{Window: window, MaxBad: maxBad}
}

// Observe records a verdict and reports whether the gate is now tripped.
// Skipped verdicts don't count either way.
func (g *Gate) Observe(v Verdict) bool {
	if v.Skipped {
		return false
	}
	g.hist = append(g.hist, !v.Healthy)
	if len(g.hist) > g.Window {
		g.hist = g.hist[len(g.hist)-g.Window:]
	}
	return g.Bad() >= g.MaxBad
}

// Bad is the number of bad verdicts in the current window.
func (g *Gate) Bad() int {
	n := 0
	for _, b := range g.hist {
		if b {
			n++
		}
	}
	return n
}

// Reset clears the history, e.g. at the start of a new wave.
func (g *Gate) Reset() { g.hist = g.hist[:0] }
