// Package chart draws stacked time-series panels as a standalone SVG. No
// dependencies, so the demo graph can be regenerated anywhere.
package chart

import (
	"bytes"
	"fmt"
	"html"
	"math"
)

// Series is one line.
type Series struct {
	Name  string
	Color string
	X, Y  []float64
	Width float64 // stroke width, default 2
	Dash  bool
}

// Marker is a labelled vertical line.
type Marker struct {
	X     float64
	Label string
	Color string
}

// Band shades an x range.
type Band struct {
	X0, X1 float64
	Label  string
	Color  string
}

// Panel is one plot area.
type Panel struct {
	Title   string
	YFmt    func(float64) string
	YMax    float64 // 0 = auto
	Series  []Series
	Markers []Marker
	Bands   []Band
	Notes   []string // printed under the title
}

// Chart is a stack of panels sharing the x axis.
type Chart struct {
	Title       string
	Subtitle    string
	XLabel      string
	XMax        float64
	Width       int
	PanelHeight int
	Panels      []Panel
	Footer      []string
}

const (
	padL   = 72.0
	padR   = 24.0
	headH  = 64.0
	titleH = 40.0
	axisH  = 34.0
	gap    = 18.0
)

// SVG renders the chart.
func (c Chart) SVG() []byte {
	if c.Width == 0 {
		c.Width = 960
	}
	if c.PanelHeight == 0 {
		c.PanelHeight = 180
	}
	w := float64(c.Width)
	ph := float64(c.PanelHeight)
	h := headH + float64(len(c.Panels))*(titleH+ph+axisH+gap) + 18*float64(len(c.Footer)) + 12

	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%.0f" viewBox="0 0 %d %.0f" font-family="-apple-system,Segoe UI,Helvetica,Arial,sans-serif">`+"\n", c.Width, h, c.Width, h)
	fmt.Fprintf(&b, `<rect width="100%%" height="100%%" fill="#ffffff"/>`+"\n")
	fmt.Fprintf(&b, `<text x="%.0f" y="28" font-size="19" font-weight="600" fill="#202124">%s</text>`+"\n", padL, esc(c.Title))
	fmt.Fprintf(&b, `<text x="%.0f" y="50" font-size="13" fill="#5f6368">%s</text>`+"\n", padL, esc(c.Subtitle))

	plotW := w - padL - padR
	sx := func(x float64) float64 { return padL + x/c.XMax*plotW }

	top := headH
	for _, p := range c.Panels {
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="14" font-weight="600" fill="#202124">%s</text>`+"\n", padL, top+16, esc(p.Title))
		for i, n := range p.Notes {
			fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="12" fill="#5f6368">%s</text>`+"\n", padL, top+32+float64(i)*14, esc(n))
		}
		y0 := top + titleH
		ymax := p.YMax
		if ymax == 0 {
			for _, s := range p.Series {
				for _, v := range s.Y {
					ymax = math.Max(ymax, v)
				}
			}
			ymax *= 1.1
		}
		if ymax == 0 {
			ymax = 1
		}
		step := nice(ymax / 4)
		ymax = math.Ceil(ymax/step) * step
		sy := func(v float64) float64 { return y0 + ph - math.Min(v, ymax)/ymax*ph }
		yf := p.YFmt
		if yf == nil {
			yf = func(v float64) string { return fmt.Sprintf("%g", v) }
		}

		for _, bd := range p.Bands {
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s" opacity="0.12"/>`+"\n",
				sx(bd.X0), y0, math.Max(1, sx(bd.X1)-sx(bd.X0)), ph, bd.Color)
			if bd.Label != "" {
				fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="11" fill="%s">%s</text>`+"\n", sx(bd.X0)+4, y0+ph-6, bd.Color, esc(bd.Label))
			}
		}
		for v := 0.0; v <= ymax+step/2; v += step {
			y := sy(v)
			fmt.Fprintf(&b, `<line x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f" stroke="#e8eaed"/>`+"\n", padL, w-padR, y, y)
			fmt.Fprintf(&b, `<text x="%.0f" y="%.1f" font-size="11" fill="#5f6368" text-anchor="end">%s</text>`+"\n", padL-8, y+4, esc(yf(v)))
		}
		xstep := nice(c.XMax / 10)
		for x := 0.0; x <= c.XMax+1e-9; x += xstep {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="11" fill="#5f6368" text-anchor="middle">%gs</text>`+"\n", sx(x), y0+ph+16, x)
		}
		fmt.Fprintf(&b, `<line x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f" stroke="#9aa0a6"/>`+"\n", padL, w-padR, y0+ph, y0+ph)

		for i, m := range p.Markers {
			x := sx(m.X)
			col := m.Color
			if col == "" {
				col = "#80868b"
			}
			fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f" stroke="%s" stroke-dasharray="4 3"/>`+"\n", x, x, y0, y0+ph, col)
			ly := y0 + 12 + float64(i%3)*13
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="11" fill="%s">%s</text>`+"\n", x+4, ly, col, esc(m.Label))
		}

		for _, s := range p.Series {
			sw := s.Width
			if sw == 0 {
				sw = 2
			}
			dash := ""
			if s.Dash {
				dash = ` stroke-dasharray="6 4"`
			}
			var pts bytes.Buffer
			for i := range s.X {
				fmt.Fprintf(&pts, "%.1f,%.1f ", sx(s.X[i]), sy(s.Y[i]))
			}
			fmt.Fprintf(&b, `<polyline fill="none" stroke="%s" stroke-width="%.1f" stroke-linejoin="round"%s points="%s"/>`+"\n", s.Color, sw, dash, pts.String())
		}

		lx := w - padR
		for i := len(p.Series) - 1; i >= 0; i-- {
			s := p.Series[i]
			tw := 7*float64(len(s.Name)) + 26
			lx -= tw
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="14" height="4" fill="%s"/>`+"\n", lx, top+10, s.Color)
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="12" fill="#3c4043">%s</text>`+"\n", lx+18, top+15, esc(s.Name))
		}

		top = y0 + ph + axisH + gap
	}
	if c.XLabel != "" {
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="11" fill="#5f6368" text-anchor="end">%s</text>`+"\n", w-padR, top-gap-4, esc(c.XLabel))
	}
	for i, f := range c.Footer {
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="12" fill="#3c4043">%s</text>`+"\n", padL, top+float64(i)*18, esc(f))
	}
	b.WriteString("</svg>\n")
	return b.Bytes()
}

// nice rounds v up to 1, 2, 2.5 or 5 times a power of ten.
func nice(v float64) float64 {
	if v <= 0 {
		return 1
	}
	exp := math.Floor(math.Log10(v))
	f := v / math.Pow(10, exp)
	var n float64
	switch {
	case f <= 1:
		n = 1
	case f <= 2:
		n = 2
	case f <= 2.5:
		n = 2.5
	case f <= 5:
		n = 5
	default:
		n = 10
	}
	return n * math.Pow(10, exp)
}

func esc(s string) string { return html.EscapeString(s) }
