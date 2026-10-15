package sim

import "time"

// Clock is a fake clock. Sleep advances time in fixed steps and runs every
// tick hook at each step, so traffic gets generated while the rollout
// engine "waits".
type Clock struct {
	now   time.Time
	start time.Time
	step  time.Duration
	hooks []func(now time.Time)
}

// NewClock starts at a fixed instant and ticks every step.
func NewClock(step time.Duration) *Clock {
	t := time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC)
	return &Clock{now: t, start: t, step: step}
}

// OnTick registers fn to run after every step.
func (c *Clock) OnTick(fn func(now time.Time)) { c.hooks = append(c.hooks, fn) }

// Now returns the fake time.
func (c *Clock) Now() time.Time { return c.now }

// Elapsed is time since the clock started.
func (c *Clock) Elapsed() time.Duration { return c.now.Sub(c.start) }

// Offset is how far t is from the clock's start.
func (c *Clock) Offset(t time.Time) time.Duration { return t.Sub(c.start) }

// Step is the tick size.
func (c *Clock) Step() time.Duration { return c.step }

// Sleep advances the clock by d, rounded up to whole steps.
func (c *Clock) Sleep(d time.Duration) {
	for d > 0 {
		c.now = c.now.Add(c.step)
		for _, h := range c.hooks {
			h(c.now)
		}
		d -= c.step
	}
}
