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

// sankeyGeom holds the shared horizontal geometry of the dashboard charts so
// the Sankey and the free-time chart below it sit on the exact same time axis.
type sankeyGeom struct {
	w, padL, padR, plotW, nodeW float64
	n                           int
}

func newSankeyGeom(n int) sankeyGeom {
	const (
		w     = 1200.0
		padL  = 42.0
		padR  = 18.0
		nodeW = 18.0
	)
	return sankeyGeom{w: w, padL: padL, padR: padR, plotW: w - padL - padR, nodeW: nodeW, n: n}
}

// nodeX returns the left edge of bucket i's node column.
func (g sankeyGeom) nodeX(i int) float64 {
	if g.n <= 1 {
		return g.padL + (g.plotW-g.nodeW)/2
	}
	return g.padL + (g.plotW-g.nodeW)*float64(i)/float64(g.n-1)
}

// centerX returns the horizontal centre of bucket i.
func (g sankeyGeom) centerX(i int) float64 { return g.nodeX(i) + g.nodeW/2 }

// colWidth returns the horizontal room available per bucket.
func (g sankeyGeom) colWidth() float64 {
	if g.n <= 1 {
		return g.plotW
	}
	return (g.plotW - g.nodeW) / float64(g.n-1)
}

// estTextWidth approximates the rendered width of a label, which is enough to
// lay out the in-chart legend without measuring actual glyphs.
func estTextWidth(s string, fontSize float64) float64 {
	return float64(len([]rune(s))) * fontSize * 0.55
}

// axisLabels appends the per-bucket week/month labels below the given baseline.
func axisLabels(b *strings.Builder, g sankeyGeom, buckets []forecast.SankeyBucket, y float64) {
	for i, bk := range buckets {
		cx := g.centerX(i)
		fmt.Fprintf(b, `<text x="%g" y="%g" font-size="11" fill="#475569" text-anchor="middle">%s</text>`,
			cx, y, template.HTMLEscapeString(bk.Label))
		if bk.SubLabel != "" {
			fmt.Fprintf(b, `<text x="%g" y="%g" font-size="9" fill="#94a3b8" text-anchor="middle">%s</text>`,
				cx, y+12, template.HTMLEscapeString(bk.SubLabel))
		}
	}
}

// sankeyLegend lays the project legend out inside the chart and returns the
// drawable entries plus the number of rows they occupy.
type sankeyLegEntry struct {
	x, row float64
	color  string
	text   string
}

func sankeyLegend(g sankeyGeom, data forecast.SankeyData, maxRows int) ([]sankeyLegEntry, int) {
	var out []sankeyLegEntry
	x, row := g.padL, 0
	for i, p := range data.Projects {
		text := fmt.Sprintf("%s · %s h", p.Name, formatHours(data.ProjectTotals[p.ID]))
		ew := 13 + estTextWidth(text, 11)
		if x > g.padL && x+ew > g.padL+g.plotW {
			if row+1 >= maxRows {
				if rest := len(data.Projects) - i; rest > 0 {
					out = append(out, sankeyLegEntry{x: x, row: float64(row), text: fmt.Sprintf("+%d weitere", rest)})
				}
				break
			}
			row++
			x = g.padL
		}
		out = append(out, sankeyLegEntry{x: x, row: float64(row), color: sanitizeColor(p.Color), text: text})
		x += ew + 16
	}
	return out, row + 1
}

// sankeySVG renders the dashboard utilization flow as a dependency-free inline
// SVG. Time buckets (weeks or months) are evenly spaced columns across the full
// width; each project forms a coloured band whose height is proportional to its
// planned hours, and adjacent buckets are joined by translucent ribbons.
// Vertical dividers separate the weeks/months and every column is annotated
// with its summed planned project hours. The project legend is drawn inside the
// chart and planned vacation shows up as a grey block in the axis strip, right
// above the week/month label. Project colours are sanitised and project names
// are HTML-escaped, so the emitted markup (returned as template.HTML) carries
// no untrusted content.
func sankeySVG(data forecast.SankeyData) template.HTML {
	const (
		h        = 376.0 // ~20 % shorter than the original 470
		headroom = 22.0  // room above the tallest column for its value label
		axisH    = 52.0  // vacation strip + week/month labels below the baseline
		legRowH  = 15.0
	)
	g := newSankeyGeom(len(data.Buckets))

	if g.n == 0 || data.MaxBucket <= 0 {
		return template.HTML(fmt.Sprintf( // #nosec G203 -- constant SVG shell, numeric values only
			`<svg viewBox="0 0 %g %g" class="sankey" role="img" aria-label="Auslastung"><text x="%g" y="%g" font-size="13" fill="#94a3b8" text-anchor="middle">Keine geplanten Stunden im gewählten Zeitraum.</text></svg>`,
			g.w, h, g.w/2, h/2))
	}

	// The legend sits inside the chart, so lay it out first: it governs how much
	// vertical room is left for the plot itself.
	legend, legRows := sankeyLegend(g, data, 2)
	legendH := 0.0
	if len(legend) > 0 {
		legendH = 6 + float64(legRows)*legRowH + 6
	}
	plotTop := legendH + headroom
	plotH := h - plotTop - axisH
	baseY := plotTop + plotH

	yMax := data.MaxBucket * 1.1
	nodeX := g.nodeX
	scaleY := func(val float64) float64 { return plotH * val / yMax }

	// Per bucket, the top/bottom Y of each project's band (bottom-aligned stack).
	type band struct{ top, bot float64 }
	bands := make([]map[string]band, g.n)
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
	fmt.Fprintf(&b, `<svg viewBox="0 0 %g %g" class="sankey" role="img" aria-label="Auslastung">`, g.w, h)

	// in-chart legend
	for _, e := range legend {
		ly := 6 + e.row*legRowH
		if e.color != "" {
			fmt.Fprintf(&b, `<rect x="%g" y="%g" width="9" height="9" rx="2" fill="%s"/>`, e.x, ly+2, e.color)
		}
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="11" fill="#475569">%s</text>`,
			e.x+13, ly+11, template.HTMLEscapeString(e.text))
	}

	// y gridlines + hour labels (0, 50%, 100% of the scale)
	for _, frac := range []float64{0, 0.5, 1} {
		val := yMax * frac
		yy := baseY - scaleY(val)
		fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#eef2f7"/>`, g.padL, yy, g.padL+g.plotW, yy)
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" fill="#94a3b8" text-anchor="end">%g</text>`, g.padL-6, yy+3, round1(val))
	}
	// left axis + baseline
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#cbd5e1"/>`, g.padL, plotTop, g.padL, baseY)
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#cbd5e1"/>`, g.padL, baseY, g.padL+g.plotW, baseY)

	// vertical dividers between the week/month columns
	for i := 0; i < g.n-1; i++ {
		x := (nodeX(i) + g.nodeW + nodeX(i+1)) / 2
		fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#e2e8f0"/>`, x, plotTop, x, baseY+4)
	}

	// ribbons between adjacent buckets (behind the nodes)
	for i := 0; i < g.n-1; i++ {
		x0 := nodeX(i) + g.nodeW
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
		cx := g.centerX(i)
		for _, p := range data.Projects {
			bd, ok := bands[i][p.ID]
			if !ok {
				continue
			}
			col := sanitizeColor(p.Color)
			fmt.Fprintf(&b,
				`<rect x="%g" y="%g" width="%g" height="%g" fill="%s" rx="1"><title>%s · %g h</title></rect>`,
				x, bd.top, g.nodeW, bd.bot-bd.top, col,
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
	}

	// planned vacation: a grey block in the axis strip, right above the label
	vacationBlocks(&b, g, data.Buckets, baseY+5, 15)
	axisLabels(&b, g, data.Buckets, baseY+34)

	b.WriteString(`</svg>`)
	return template.HTML(b.String()) // #nosec G203 -- sanitised colours + escaped names; other values numeric/controlled
}

// vacationBlocks draws a grey block per bucket that has planned vacation, laid
// out like the week/month label right below it.
func vacationBlocks(b *strings.Builder, g sankeyGeom, buckets []forecast.SankeyBucket, y, height float64) {
	bw := g.colWidth() * 0.86
	if bw > 130 {
		bw = 130
	}
	for i, bk := range buckets {
		if bk.VacationHours <= 0 {
			continue
		}
		cx := g.centerX(i)
		fmt.Fprintf(b, `<rect x="%g" y="%g" width="%g" height="%g" rx="3" fill="#cbd5e1"><title>Urlaub geplant: %s h (%s Tage)</title></rect>`,
			cx-bw/2, y, bw, height, formatHours(bk.VacationHours), formatHours(bk.VacationDays))
		label := "Urlaub " + formatHours(bk.VacationHours) + " h"
		if estTextWidth(label, 9) > bw-8 {
			label = formatHours(bk.VacationHours) + " h"
		}
		if estTextWidth(label, 9) <= bw-4 {
			fmt.Fprintf(b, `<text x="%g" y="%g" font-size="9" fill="#334155" text-anchor="middle">%s</text>`,
				cx, y+height-4, label)
		}
	}
}

// freeTimeSVG renders the remaining free working time per bucket as a column
// chart on the same time axis as the Sankey above it: capacity (weekdays minus
// public holidays and planned vacation) minus the planned project hours.
// Columns below the zero line mark an overbooked bucket.
func freeTimeSVG(data forecast.SankeyData) template.HTML {
	const (
		h    = 190.0
		padT = 26.0
		padB = 46.0
	)
	g := newSankeyGeom(len(data.Buckets))
	plotH := h - padT - padB
	baseY := padT + plotH

	if g.n == 0 {
		return template.HTML(fmt.Sprintf( // #nosec G203 -- constant SVG shell, numeric values only
			`<svg viewBox="0 0 %g %g" class="freetime" role="img" aria-label="Freie Zeit"><text x="%g" y="%g" font-size="13" fill="#94a3b8" text-anchor="middle">Kein Zeitraum gewählt.</text></svg>`,
			g.w, h, g.w/2, h/2))
	}

	maxPos, maxNeg := 0.0, 0.0
	for _, bk := range data.Buckets {
		if bk.FreeHours > maxPos {
			maxPos = bk.FreeHours
		}
		if -bk.FreeHours > maxNeg {
			maxNeg = -bk.FreeHours
		}
	}
	span := (maxPos + maxNeg) * 1.15
	if span <= 0 {
		span = 1
	}
	// Keep a strip free below the deepest column so its value label never runs
	// into the week/month labels underneath.
	usableH := plotH - 16
	zeroY := padT + usableH*(maxPos*1.15)/span
	scale := func(v float64) float64 { return usableH * v / span }

	barW := g.colWidth() * 0.6
	if barW > 54 {
		barW = 54
	}
	if barW < 4 {
		barW = 4
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %g %g" class="freetime" role="img" aria-label="Freie Zeit">`, g.w, h)

	// legend (top left) + zero line
	b.WriteString(`<rect x="42" y="4" width="9" height="9" rx="2" fill="#38bdf8"/>` +
		`<text x="55" y="13" font-size="11" fill="#475569">Freie Zeit</text>` +
		`<rect x="134" y="4" width="9" height="9" rx="2" fill="#ef4444"/>` +
		`<text x="147" y="13" font-size="11" fill="#475569">Überbucht</text>`)
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#cbd5e1"/>`, g.padL, zeroY, g.padL+g.plotW, zeroY)
	fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" fill="#94a3b8" text-anchor="end">0</text>`, g.padL-6, zeroY+3)

	for i, bk := range data.Buckets {
		cx := g.centerX(i)
		v := bk.FreeHours
		bh := scale(v)
		if bh < 0 {
			bh = -bh
		}
		y, col, labelY := zeroY-bh, "#38bdf8", zeroY-bh-5
		switch {
		case v < 0:
			y, col, labelY = zeroY, "#ef4444", zeroY+bh+12
		case v == 0:
			bh, y, col, labelY = 2, zeroY-1, "#e2e8f0", zeroY-6
		}
		fmt.Fprintf(&b, `<rect x="%g" y="%g" width="%g" height="%g" rx="2" fill="%s"><title>%s · %s h frei (Kapazität %s h, geplant %s h)</title></rect>`,
			cx-barW/2, y, barW, bh, col,
			template.HTMLEscapeString(bk.Label), formatHours(v), formatHours(bk.CapacityHours), formatHours(bk.Total))
		textFill := "#475569"
		if v < 0 {
			textFill = "#b91c1c"
		}
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" font-weight="600" fill="%s" text-anchor="middle">%s</text>`,
			cx, labelY, textFill, formatHours(v))
	}

	axisLabels(&b, g, data.Buckets, baseY+16)

	b.WriteString(`</svg>`)
	return template.HTML(b.String()) // #nosec G203 -- escaped labels; other values numeric/controlled
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
