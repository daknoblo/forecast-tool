package web

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/models"
)

// sanitizeColor returns a safe hex colour for direct inclusion in SVG markup,
// falling back to the default accent when the input is not a valid #RGB/#RRGGBB
// value. Because the generated SVG is emitted as template.HTML (bypassing the
// auto-escaping of html/template), untrusted colour strings could otherwise
// break out of the attribute and inject markup.
func sanitizeColor(c string) string {
	c = strings.TrimSpace(c)
	if !models.IsHexColor(c) {
		return "#2563eb"
	}
	return c
}

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
	color = sanitizeColor(color)
	plotW := w - padL - padR
	plotH := h - padT - padB

	n := len(points)
	if n < 2 {
		// Not enough data to draw a meaningful curve; numeric-only placeholder.
		return template.HTML(fmt.Sprintf( // #nosec G203 -- constant SVG shell, numeric values only
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

// progressSVG renders a compact cumulative burn-up chart: the cumulative
// projected hours across the sub-periods (months) versus the ideal even pace
// towards the target, plus a horizontal target line. Slope conveys the burn
// rate, the gap to the ideal line conveys progress and the fill relative to the
// target line conveys utilization. Inputs are numeric plus controlled month
// labels, so the inline SVG carries no untrusted markup.
func progressSVG(labels []string, cumulative []float64, target float64) template.HTML {
	const (
		w    = 640.0
		h    = 220.0
		padL = 44.0
		padR = 12.0
		padT = 14.0
		padB = 40.0
	)
	n := len(cumulative)
	plotW := w - padL - padR
	plotH := h - padT - padB
	if n < 1 {
		return template.HTML(fmt.Sprintf( // #nosec G203 -- constant SVG shell, numeric values only
			`<svg viewBox="0 0 %g %g" class="progress-chart" role="img" aria-label="Fortschritt"></svg>`, w, h))
	}
	yMax := target
	for _, v := range cumulative {
		if v > yMax {
			yMax = v
		}
	}
	if yMax <= 0 {
		yMax = 1
	}
	x := func(i int) float64 {
		if n == 1 {
			return padL + plotW/2
		}
		return padL + plotW*float64(i)/float64(n-1)
	}
	y := func(val float64) float64 {
		if val < 0 {
			val = 0
		}
		return padT + plotH*(1-val/yMax)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %g %g" class="progress-chart" role="img" aria-label="Fortschritt">`, w, h)

	// axes
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#cbd5e1"/>`, padL, padT, padL, padT+plotH)
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#cbd5e1"/>`, padL, padT+plotH, padL+plotW, padT+plotH)

	// y gridlines / labels (0, 50%, 100% of the scale max)
	for _, frac := range []float64{0, 0.5, 1} {
		val := yMax * frac
		yy := y(val)
		fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#eef2f7"/>`, padL, yy, padL+plotW, yy)
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" fill="#64748b" text-anchor="end">%g</text>`, padL-6, yy+3, round1(val))
	}

	// target line (green, dashed)
	if target > 0 && target <= yMax {
		ty := y(target)
		fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#16a34a" stroke-dasharray="5 4"/>`, padL, ty, padL+plotW, ty)
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" fill="#16a34a" text-anchor="end">Ziel %g</text>`, padL+plotW, ty-4, round1(target))
	}

	// ideal even-pace line (0 at the start -> target at the end)
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#94a3b8" stroke-dasharray="4 4"/>`, x(0), y(0), x(n-1), y(target))

	// cumulative projected polyline
	var pts strings.Builder
	for i, v := range cumulative {
		fmt.Fprintf(&pts, "%g,%g ", x(i), y(v))
	}
	fmt.Fprintf(&b, `<polyline fill="none" stroke="#2563eb" stroke-width="2" points="%s"/>`, strings.TrimSpace(pts.String()))

	// x labels (sub-period labels, thinned to avoid crowding)
	step := (n-1)/8 + 1
	for i := 0; i < n; i++ {
		if i%step == 0 || i == n-1 {
			lbl := ""
			if i < len(labels) {
				lbl = shortLabel(labels[i])
			}
			fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" fill="#64748b" text-anchor="middle">%s</text>`, x(i), padT+plotH+16, lbl)
		}
	}

	b.WriteString(`</svg>`)
	return template.HTML(b.String()) // #nosec G203 -- numeric values + controlled month labels only
}

// sankeySVG renders the dashboard utilization flow as a dependency-free inline
// SVG. Time buckets (weeks or months) are evenly spaced columns across the full
// width; each project forms a coloured band whose height is proportional to its
// planned hours, and adjacent buckets are joined by translucent ribbons.
// Vertical dividers separate the weeks/months and every column is annotated
// with its summed planned project hours. Project colours are sanitised and
// project names are HTML-escaped inside <title> tooltips, so the emitted markup
// (returned as template.HTML) carries no untrusted content.
func sankeySVG(data forecast.SankeyData) template.HTML {
	const (
		w     = 1200.0
		h     = 470.0
		padL  = 42.0
		padR  = 18.0
		padT  = 30.0
		padB  = 48.0
		nodeW = 18.0
	)
	n := len(data.Buckets)
	plotW := w - padL - padR
	plotH := h - padT - padB
	baseY := padT + plotH

	if n == 0 || data.MaxBucket <= 0 {
		return template.HTML(fmt.Sprintf( // #nosec G203 -- constant SVG shell, numeric values only
			`<svg viewBox="0 0 %g %g" class="sankey" role="img" aria-label="Auslastung"><text x="%g" y="%g" font-size="13" fill="#94a3b8" text-anchor="middle">Keine geplanten Stunden im gewählten Zeitraum.</text></svg>`,
			w, h, w/2, padT+plotH/2))
	}

	yMax := data.MaxBucket * 1.1
	nodeX := func(i int) float64 {
		if n == 1 {
			return padL + (plotW-nodeW)/2
		}
		return padL + (plotW-nodeW)*float64(i)/float64(n-1)
	}
	scaleY := func(val float64) float64 { return plotH * val / yMax }

	// Per bucket, the top/bottom Y of each project's band (bottom-aligned stack).
	type band struct{ top, bot float64 }
	bands := make([]map[string]band, n)
	for i, bk := range data.Buckets {
		bands[i] = make(map[string]band, len(bk.Hours))
		y := baseY
		for _, p := range data.Projects {
			hh := bk.Hours[p.ID]
			if hh <= 0 {
				continue
			}
			bh := scaleY(hh)
			bands[i][p.ID] = band{top: y - bh, bot: y}
			y -= bh
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %g %g" class="sankey" role="img" aria-label="Auslastung">`, w, h)

	// y gridlines + hour labels (0, 50%, 100% of the scale)
	for _, frac := range []float64{0, 0.5, 1} {
		val := yMax * frac
		yy := baseY - scaleY(val)
		fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#eef2f7"/>`, padL, yy, padL+plotW, yy)
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" fill="#94a3b8" text-anchor="end">%g</text>`, padL-6, yy+3, round1(val))
	}
	// left axis + baseline
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#cbd5e1"/>`, padL, padT, padL, baseY)
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#cbd5e1"/>`, padL, baseY, padL+plotW, baseY)

	// vertical dividers between the week/month columns
	for i := 0; i < n-1; i++ {
		x := (nodeX(i) + nodeW + nodeX(i+1)) / 2
		fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#e2e8f0"/>`, x, padT, x, baseY+4)
	}

	// ribbons between adjacent buckets (behind the nodes)
	for i := 0; i < n-1; i++ {
		x0 := nodeX(i) + nodeW
		x1 := nodeX(i + 1)
		xc := (x0 + x1) / 2
		for _, p := range data.Projects {
			a, okA := bands[i][p.ID]
			c, okC := bands[i+1][p.ID]
			if !okA || !okC {
				continue
			}
			col := sanitizeColor(p.Color)
			fmt.Fprintf(&b,
				`<path d="M%g %g C%g %g %g %g %g %g L%g %g C%g %g %g %g %g %g Z" fill="%s" fill-opacity="0.3"/>`,
				x0, a.top, xc, a.top, xc, c.top, x1, c.top,
				x1, c.bot, xc, c.bot, xc, a.bot, x0, a.bot, col)
		}
	}

	// nodes (stacked project bands) + column annotations
	for i, bk := range data.Buckets {
		x := nodeX(i)
		cx := x + nodeW/2
		for _, p := range data.Projects {
			bd, ok := bands[i][p.ID]
			if !ok {
				continue
			}
			col := sanitizeColor(p.Color)
			fmt.Fprintf(&b,
				`<rect x="%g" y="%g" width="%g" height="%g" fill="%s" rx="1"><title>%s · %g h</title></rect>`,
				x, bd.top, nodeW, bd.bot-bd.top, col,
				template.HTMLEscapeString(p.Name), round1(bk.Hours[p.ID]))
		}
		// summed planned hours above the stack (muted when empty)
		top := baseY - scaleY(bk.Total)
		fill := "#334155"
		if bk.Total == 0 {
			fill = "#cbd5e1"
			top = baseY
		}
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="11" font-weight="600" fill="%s" text-anchor="middle">%g</text>`,
			cx, top-6, fill, round1(bk.Total))
		// x-axis labels (week/month + sub label)
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="11" fill="#475569" text-anchor="middle">%s</text>`, cx, baseY+16, bk.Label)
		if bk.SubLabel != "" {
			fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="9" fill="#94a3b8" text-anchor="middle">%s</text>`, cx, baseY+29, bk.SubLabel)
		}
	}

	b.WriteString(`</svg>`)
	return template.HTML(b.String()) // #nosec G203 -- sanitised colours + escaped names; other values numeric/controlled
}

// shortLabel trims a label to its first three runes for compact chart axes.
func shortLabel(s string) string {
	r := []rune(s)
	if len(r) > 3 {
		return string(r[:3])
	}
	return s
}

// cumulative returns the running sum of vals (each rounded to 0.1).
func cumulative(vals []float64) []float64 {
	out := make([]float64, len(vals))
	run := 0.0
	for i, v := range vals {
		run += v
		out[i] = round1(run)
	}
	return out
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
