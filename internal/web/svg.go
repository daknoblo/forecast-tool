package web

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/daknoblo/forecast-tool/internal/forecast"
)

// burndownSVG renders a simple, dependency-free burn-down chart as inline SVG.
// The X axis is the calendar week (with month labels), the Y axis is the
// remaining budget in hours.
func burndownSVG(points []forecast.BurnPoint, budget float64, color string) template.HTML {
	const (
		w    = 720.0
		h    = 256.0
		padL = 44.0
		padR = 16.0
		padT = 16.0
		padB = 44.0
	)
	if color == "" {
		color = "#2563eb"
	}
	plotW := w - padL - padR
	plotH := h - padT - padB

	n := len(points)
	if n < 2 {
		// Not enough data to draw a meaningful curve.
		return template.HTML(fmt.Sprintf(
			`<svg viewBox="0 0 %g %g" class="burndown" role="img" aria-label="Burn-Down"></svg>`, w, h))
	}
	yMax := budget
	if yMax <= 0 {
		yMax = 1
	}

	x := func(i int) float64 { return padL + plotW*float64(i)/float64(n-1) }
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

	// ideal line (full budget at the start -> 0 at the end)
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#94a3b8" stroke-dasharray="4 4"/>`,
		x(0), y(budget), x(n-1), y(0))

	// actual remaining polyline
	var pts strings.Builder
	for i, p := range points {
		fmt.Fprintf(&pts, "%g,%g ", x(i), y(p.Remaining))
	}
	fmt.Fprintf(&b, `<polyline fill="none" stroke="%s" stroke-width="2" points="%s"/>`, color, strings.TrimSpace(pts.String()))

	// x labels: KW every ~step points, plus a month label whenever the month changes.
	step := (n - 1) / 6
	if step < 1 {
		step = 1
	}
	prevMonth := 0
	for i, p := range points {
		xx := x(i)
		if i%step == 0 || i == n-1 {
			fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" fill="#64748b" text-anchor="middle">KW%02d</text>`,
				xx, padT+plotH+15, p.ISOWeek)
		}
		if p.Month != prevMonth {
			prevMonth = p.Month
			label := monthAbbrev(p.Month)
			if p.Month == 1 {
				label = fmt.Sprintf("%s %d", label, p.Year)
			}
			fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#e2e8f0"/>`, xx, padT, xx, padT+plotH)
			fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" font-weight="600" fill="#475569" text-anchor="middle">%s</text>`,
				xx, padT+plotH+30, label)
		}
	}

	b.WriteString(`</svg>`)
	return template.HTML(b.String()) // #nosec G203 -- numeric values only, no user input
}

var monthAbbrevDE = []string{
	"Jan", "Feb", "Mär", "Apr", "Mai", "Jun",
	"Jul", "Aug", "Sep", "Okt", "Nov", "Dez",
}

func monthAbbrev(m int) string {
	if m < 1 || m > 12 {
		return ""
	}
	return monthAbbrevDE[m-1]
}

func round1(f float64) float64 {
	return float64(int64(f*10+0.5)) / 10
}
