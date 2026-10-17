package demo

import (
	"fmt"
	"math"

	"github.com/youssefm1/rollguard/internal/chart"
)

const (
	red    = "#d93025"
	gray   = "#80868b"
	blue   = "#1a73e8"
	orange = "#e8710a"
	green  = "#188038"
)

// Graph draws the progressive run against the all-at-once run.
func Graph(sc Scenario, prog, bb Run) []byte {
	xmax := 0.0
	for _, r := range []Run{prog, bb} {
		if n := len(r.Series); n > 0 {
			xmax = math.Max(xmax, r.Series[n-1].T)
		}
	}

	pct := func(v float64) string { return trimPct(v * 100) }
	msf := func(v float64) string { return fmt.Sprintf("%.0fms", v) }

	latency := sc.Signal == "latency"
	pick := func(p Point, canary bool) float64 {
		switch {
		case latency && canary:
			return p.CanaryP99
		case latency:
			return p.ControlP99
		case canary:
			return p.CanaryErr
		}
		return p.ControlErr
	}
	fleet := func(p Point) float64 {
		if latency {
			return p.FleetP99
		}
		return p.FleetErr
	}
	yf := pct
	what := "error rate"
	if latency {
		yf = msf
		what = "p99 latency"
	}

	// Panel 1: the signal the watcher actually judged.
	var can, ctl chart.Series
	can = chart.Series{Name: "cells on new config", Color: red}
	ctl = chart.Series{Name: "control (everyone else)", Color: gray, Width: 1.5}
	for _, p := range prog.Series {
		if p.OnNew > 0 {
			can.X = append(can.X, p.T)
			can.Y = append(can.Y, pick(p, true))
		}
		ctl.X = append(ctl.X, p.T)
		ctl.Y = append(ctl.Y, pick(p, false))
	}
	var markers []chart.Marker
	for _, m := range prog.Marks {
		col := gray
		switch m.Kind {
		case "breach":
			col = red
		case "recovered":
			col = green
		}
		markers = append(markers, chart.Marker{X: m.T, Label: m.Label, Color: col})
	}
	var bands []chart.Band
	if prog.RollbackAt > 0 {
		bands = append(bands, chart.Band{X0: prog.PushAt, X1: prog.RollbackAt, Color: red,
			Label: fmt.Sprintf("%d cells exposed for %.0fs", prog.Result.ExposedCells, prog.RollbackAt-prog.PushAt)})
	}
	p1 := chart.Panel{
		Title:   "Progressive rollout: " + what + " on canary vs. control",
		YFmt:    yf,
		Series:  []chart.Series{ctl, can},
		Markers: markers,
		Bands:   bands,
	}

	// Panel 2: what users across the whole fleet saw.
	fp := chart.Series{Name: "progressive (1/10/50/100)", Color: blue}
	fb := chart.Series{Name: "all at once", Color: orange}
	for _, p := range prog.Series {
		fp.X = append(fp.X, p.T)
		fp.Y = append(fp.Y, fleet(p))
	}
	for _, p := range bb.Series {
		fb.X = append(fb.X, p.T)
		fb.Y = append(fb.Y, fleet(p))
	}
	p2 := chart.Panel{
		Title:  "Fleet-wide " + what + ": same watcher, same rollback, only the wave plan differs",
		YFmt:   yf,
		Series: []chart.Series{fb, fp},
	}

	// Panel 3: blast radius.
	ep := chart.Series{Name: "progressive", Color: blue}
	eb := chart.Series{Name: "all at once", Color: orange}
	for _, p := range prog.Series {
		ep.X = append(ep.X, p.T)
		ep.Y = append(ep.Y, p.OnNew)
	}
	for _, p := range bb.Series {
		eb.X = append(eb.X, p.T)
		eb.Y = append(eb.Y, p.OnNew)
	}
	p3 := chart.Panel{
		Title:  "Share of cells running the new config",
		YFmt:   pct,
		YMax:   1,
		Series: []chart.Series{eb, ep},
	}

	footer := []string{
		fmt.Sprintf("progressive: %s, %d/%d cells exposed, peak fleet %s %s, %s failed requests above baseline",
			outcome(prog), prog.Result.ExposedCells, prog.Cells, what, peak(prog, latency), comma(prog.ExcessErrors())),
		fmt.Sprintf("all at once: %s, %d/%d cells exposed, peak fleet %s %s, %s failed requests above baseline",
			outcome(bb), bb.Result.ExposedCells, bb.Cells, what, peak(bb, latency), comma(bb.ExcessErrors())),
	}

	c := chart.Chart{
		Title:    fmt.Sprintf("rollguard chaos demo: %q", sc.Name),
		Subtitle: sc.Story,
		XLabel:   "simulated time",
		XMax:     xmax,
		Panels:   []chart.Panel{p1, p2, p3},
		Footer:   footer,
	}
	return c.SVG()
}

func outcome(r Run) string {
	if r.Result.Outcome == "completed" {
		return "completed"
	}
	return fmt.Sprintf("caught in wave %d after %.0fs", r.Result.FailedWave, r.Result.TimeToDetect().Seconds())
}

func peak(r Run, latency bool) string {
	if !latency {
		return trimPct(r.PeakFleetErr() * 100)
	}
	m := 0.0
	for _, p := range r.Series {
		m = math.Max(m, p.FleetP99)
	}
	return fmt.Sprintf("%.0fms", m)
}

func trimPct(v float64) string {
	switch {
	case v == 0:
		return "0%"
	case v < 1:
		return fmt.Sprintf("%.2f%%", v)
	case v < 10:
		return fmt.Sprintf("%.1f%%", v)
	}
	return fmt.Sprintf("%.0f%%", v)
}

func comma(v float64) string {
	n := int64(math.Round(v))
	s := fmt.Sprint(n)
	out := ""
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out += ","
		}
		out += string(c)
	}
	return out
}
