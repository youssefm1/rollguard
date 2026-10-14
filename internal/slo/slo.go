// Package slo decides whether a set of cells is healthy compared to the
// rest of the fleet.
package slo

import (
	"fmt"
	"time"
)

// Sample is aggregated traffic for a group of cells over one window.
type Sample struct {
	Cells    int
	Requests float64
	Errors   float64
	// P99 is a request-weighted average of per-cell p99s. Not a true
	// fleet p99, but good enough to compare canary against control.
	P99 time.Duration
}

// ErrorRate is errors/requests, or 0 with no traffic.
func (s Sample) ErrorRate() float64 {
	if s.Requests <= 0 {
		return 0
	}
	return s.Errors / s.Requests
}

// Objective is what "healthy" means. A breach needs both the absolute
// limit and the relative limit against control. The relative check keeps a
// fleet-wide problem we didn't cause (an upstream outage, say) from
// rolling back a good change. With no control group, only the absolute
// limit applies.
type Objective struct {
	MaxErrorRate    float64       // e.g. 0.01 for 1%
	MaxP99          time.Duration // e.g. 250ms
	MaxErrorRatio   float64       // canary/control, e.g. 2.0
	MaxLatencyRatio float64       // canary/control, e.g. 1.5
	MinRequests     float64       // below this the sample is ignored
}

// DefaultObjective is a reasonable starting point for the demo service.
var DefaultObjective = Objective{
	MaxErrorRate:    0.01,
	MaxP99:          250 * time.Millisecond,
	MaxErrorRatio:   2.0,
	MaxLatencyRatio: 1.5,
	MinRequests:     500,
}

// Verdict is the result of comparing one canary sample to control.
type Verdict struct {
	Healthy bool
	Skipped bool // not enough traffic to judge
	Reasons []string
}

func (v Verdict) String() string {
	switch {
	case v.Skipped:
		return "skip"
	case v.Healthy:
		return "ok"
	}
	return fmt.Sprintf("BAD %v", v.Reasons)
}

// Compare judges canary against control. control.Cells == 0 means there is
// no control group left (the final wave).
func (o Objective) Compare(canary, control Sample) Verdict {
	if canary.Requests < o.MinRequests {
		return Verdict{Healthy: true, Skipped: true}
	}
	hasControl := control.Cells > 0 && control.Requests >= o.MinRequests
	v := Verdict{Healthy: true}

	er := canary.ErrorRate()
	if er > o.MaxErrorRate {
		if !hasControl || er > control.ErrorRate()*o.MaxErrorRatio {
			v.Healthy = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("error rate %.2f%% (slo %.2f%%, control %.2f%%)",
				er*100, o.MaxErrorRate*100, control.ErrorRate()*100))
		}
	}
	if o.MaxP99 > 0 && canary.P99 > o.MaxP99 {
		if !hasControl || float64(canary.P99) > float64(control.P99)*o.MaxLatencyRatio {
			v.Healthy = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("p99 %v (slo %v, control %v)",
				canary.P99.Round(time.Millisecond), o.MaxP99, control.P99.Round(time.Millisecond)))
		}
	}
	return v
}
