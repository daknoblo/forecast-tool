package web

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/daknoblo/forecast-tool/internal/forecast"
)

// burndownSVG renders a simple, dependency-free burn-down chart as inline SVG.
// The X axis is the ISO week, the Y axis is the remaining budget in hours.
func burndownSVG(points []forecast.BurnPoint, budget float64, color string) template.HTML {
	const (
		w        = 720.0
		h        = 240.0
		padL     = 44.0
		padR     = 16.0
		padT     = 16.0
		padB     = 28.0
	)
	if color == "" {
		color = "#2563eb"
	}
	plotW := w - padL - padR
	plotH := h - padT - padB

	maxWeek := 1
	if n := len(points); n > 0 {
		maxWeek = points[n-1].Week
	}
	if maxWeek < 1 {
		maxWeek = 1
	}
	yMax := budget
	if yMax <= 0 {
		yMax = 1
	}

	x := func(week int) float64 { return padL + plotW*float64(week)/float64(maxWeek) }
	y := func(val float64) float64 {
		v := val
		if v < 0 {
			v = 0
		}
		return padT + plotH*(1-v/yMax)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %g %g" class="burndown" role="img" aria-label="Burn-Down">`, w, h)

	// axes
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#cbd5e1"/>`, padL, padT, padL, padT+plotH)
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#cbd5e1"/>`, padL, padT+plotH, padL+plotW, padT+plotH)

	// y gridlines / labels (0, 50%, 100% of budget)
	for _, frac := range []float64{0, 0.5, 1} {
		val := budget * frac
		yy := y(val)
		fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#eef2f7"/>`, padL, yy, padL+plotW, yy)
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" fill="#64748b" text-anchor="end">%g</text>`, padL-6, yy+3, round1(val))
	}

	// ideal line (full budget -> 0 across the year)
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#94a3b8" stroke-dasharray="4 4"/>`,
		x(0), y(budget), x(maxWeek), y(0))

	// actual remaining polyline
	var pts strings.Builder
	for _, p := range points {
		fmt.Fprintf(&pts, "%g,%g ", x(p.Week), y(p.Remaining))
	}
	fmt.Fprintf(&b, `<polyline fill="none" stroke="%s" stroke-width="2" points="%s"/>`, color, strings.TrimSpace(pts.String()))

	// x labels (every ~13 weeks)
	step := maxWeek / 4
	if step < 1 {
		step = 1
	}
	for wk := 0; wk <= maxWeek; wk += step {
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" fill="#64748b" text-anchor="middle">KW%d</text>`,
			x(wk), padT+plotH+16, wk)
	}

	b.WriteString(`</svg>`)
	return template.HTML(b.String()) // #nosec G203 -- numeric values only, no user input
}

func round1(f float64) float64 {
	return float64(int64(f*10+0.5)) / 10
}
