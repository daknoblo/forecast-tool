package web

import (
	"fmt"
	"html/template"
	"math"
	"sort"
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
// remaining budget in hours. In private mode the axis figures are masked, so
// only the shape of the curve remains visible.
func burndownSVG(points []forecast.BurnPoint, budget float64, color string, private bool) template.HTML {
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
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" fill="#64748b" text-anchor="end">%s</text>`, padL-6, yy+3, chartHours(round1(val), private))
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

// niceStep returns a rounded axis step (1/2/2.5/5 x 10^k) that splits max into
// roughly the requested number of gridlines.
func niceStep(max float64, want int) float64 {
	if max <= 0 || want < 1 {
		return 1
	}
	raw := max / float64(want)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if step := m * mag; step >= raw {
			return step
		}
	}
	return 10 * mag
}

// progressSVG renders a compact cumulative burn-up chart of one period against
// the evenly paced ideal and the period's target. It draws two series: the
// cumulative booked hours (solid, filled) and the cumulative projection
// (booked + forecast, dashed). Splitting by "completed sub-periods" instead
// would hide the hours already booked in the running month. `done` only marks
// the last finished sub-period on the x axis. Inputs are numeric plus controlled
// month labels, so the inline SVG carries no untrusted markup. In private mode
// every figure is masked.
func progressSVG(labels []string, booked, projected []float64, target float64, done int, private bool) template.HTML {
	const (
		w    = 560.0
		h    = 232.0
		padL = 48.0
		padR = 14.0
		padT = 30.0
		padB = 40.0
	)
	n := len(projected)
	plotW := w - padL - padR
	plotH := h - padT - padB
	if n < 1 {
		return template.HTML(fmt.Sprintf( // #nosec G203 -- constant SVG shell, numeric values only
			`<svg viewBox="0 0 %g %g" class="progress-chart" role="img" aria-label="Fortschritt"></svg>`, w, h))
	}
	if done < 0 {
		done = 0
	}
	if done > n {
		done = n
	}
	peak := target
	maxBooked := 0.0
	for i, v := range projected {
		if v > peak {
			peak = v
		}
		if i < len(booked) && booked[i] > maxBooked {
			maxBooked = booked[i]
		}
	}
	if maxBooked > peak {
		peak = maxBooked
	}
	if peak <= 0 {
		peak = 1
	}
	step := niceStep(peak, 4)
	yMax := math.Ceil(peak/step) * step
	if yMax <= 0 {
		yMax = step
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
	// The i-th point is the state AFTER sub-period i+1, so the even pace has to
	// be measured against i+1 of n sub-periods - otherwise the ideal line would
	// start a whole period behind the actual curve.
	ideal := func(i int) float64 { return target * float64(i+1) / float64(n) }

	const (
		colDone      = "#0e7490"
		colProjected = "#1d4ed8"
		colIdeal     = "#475569"
		colTarget    = "#15803d"
		colGrid      = "#e2e8f0"
		colAxis      = "#94a3b8"
	)

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %g %g" class="progress-chart" role="img" aria-label="Fortschritt">`, w, h)

	legend := []struct{ color, text string }{
		{colDone, "Gebucht"}, {colProjected, "Hochrechnung"}, {colIdeal, "Ideal"}, {colTarget, "Ziel"},
	}
	lx := padL
	for _, l := range legend {
		fmt.Fprintf(&b, `<rect x="%g" y="5" width="9" height="9" rx="2" fill="%s"/>`, lx, l.color)
		fmt.Fprintf(&b, `<text x="%g" y="14" font-size="11" fill="#475569">%s</text>`, lx+13, l.text)
		lx += 26 + estTextWidth(l.text, 11)
	}

	for v := 0.0; v <= yMax+step/2; v += step {
		yy := y(v)
		fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="%s"/>`, padL, yy, padL+plotW, yy, colGrid)
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="11" fill="#475569" text-anchor="end">%s</text>`,
			padL-7, yy+4, chartHours(round1(v), private))
	}
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="%s"/>`, padL, padT, padL, padT+plotH, colAxis)
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="%s"/>`, padL, padT+plotH, padL+plotW, padT+plotH, colAxis)

	if target > 0 && target <= yMax {
		ty := y(target)
		fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="%s" stroke-width="1.5" stroke-dasharray="6 4"/>`,
			padL, ty, padL+plotW, ty, colTarget)
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="11" font-weight="600" fill="%s">Ziel %s h</text>`,
			padL+5, ty-5, colTarget, chartHours(round1(target), private))
	}

	// ideal even pace: dotted so it never reads as a second forecast curve
	var idealPts strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&idealPts, "%g,%g ", x(i), y(ideal(i)))
	}
	fmt.Fprintf(&b, `<polyline fill="none" stroke="%s" stroke-width="2" stroke-dasharray="2 4" stroke-linecap="round" points="%s"/>`,
		colIdeal, strings.TrimSpace(idealPts.String()))

	// projection first, the booked series is painted on top of it
	var projPts strings.Builder
	for i, v := range projected {
		fmt.Fprintf(&projPts, "%g,%g ", x(i), y(v))
	}
	fmt.Fprintf(&b, `<polyline fill="none" stroke="%s" stroke-width="2.5" stroke-dasharray="5 3" points="%s"/>`,
		colProjected, strings.TrimSpace(projPts.String()))

	if maxBooked > 0 {
		var area, line strings.Builder
		fmt.Fprintf(&area, "%g,%g ", x(0), padT+plotH)
		for i := range projected {
			v := 0.0
			if i < len(booked) {
				v = booked[i]
			}
			fmt.Fprintf(&area, "%g,%g ", x(i), y(v))
			fmt.Fprintf(&line, "%g,%g ", x(i), y(v))
		}
		fmt.Fprintf(&area, "%g,%g", x(n-1), padT+plotH)
		fmt.Fprintf(&b, `<polygon fill="%s" fill-opacity="0.16" points="%s"/>`, colDone, area.String())
		fmt.Fprintf(&b, `<polyline fill="none" stroke="%s" stroke-width="2.5" points="%s"/>`,
			colDone, strings.TrimSpace(line.String()))
	}

	for i, v := range projected {
		bv := 0.0
		if i < len(booked) {
			bv = booked[i]
		}
		fmt.Fprintf(&b, `<circle cx="%g" cy="%g" r="2.6" fill="%s"><title>%s: %s h Hochrechnung, davon %s h gebucht</title></circle>`,
			x(i), y(v), colProjected, template.HTMLEscapeString(shortLabel(labelAt(labels, i))),
			chartHours(round1(v), private), chartHours(round1(bv), private))
	}

	// x labels with a tick each, thinned only when they would collide
	stepX := 1
	if n > 1 {
		if per := plotW / float64(n-1); per < 34 {
			stepX = int(34/per) + 1
		}
	}
	for i := 0; i < n; i++ {
		if i%stepX != 0 && i != n-1 {
			continue
		}
		fill := "#475569"
		weight := "400"
		if i == done-1 {
			fill, weight = colDone, "600" // last completed sub-period
		}
		fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="%s"/>`, x(i), padT+plotH, x(i), padT+plotH+4, colAxis)
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="11" font-weight="%s" fill="%s" text-anchor="middle">%s</text>`,
			x(i), padT+plotH+18, weight, fill, template.HTMLEscapeString(shortLabel(labelAt(labels, i))))
	}

	b.WriteString(`</svg>`)
	return template.HTML(b.String()) // #nosec G203 -- numeric values + controlled month labels only
}

// labelAt returns the i-th label or an empty string when it is missing.
func labelAt(labels []string, i int) string {
	if i < len(labels) {
		return labels[i]
	}
	return ""
}

// goalFlowTitle builds the escaped, multi-line tooltip of a flow node: planned
// vs. already booked hours and, for the periods, the evenly split goal.
func goalFlowTitle(n forecast.GoalFlowNode, private bool) string {
	head := template.HTMLEscapeString(n.Title)
	if n.StateLabel != "" {
		head += " · " + template.HTMLEscapeString(n.StateLabel)
	}
	body := fmt.Sprintf("%s h geplant · %s h gebucht", chartHours(n.Hours, private), chartHours(n.Booked, private))
	if n.Target > 0 {
		body += fmt.Sprintf("&#10;Soll %s h · %s %% erreicht", chartHours(n.Target, private), chartHours(n.PctOfTarget, private))
	}
	return head + "&#10;" + body
}

// goalFlowSVG renders the fiscal year's hours as a five-stage Sankey: projects
// feed the months, the months their quarter, the quarters their half-year and
// both halves the whole year. Every stage carries the same total, so all columns
// are equally tall and only the split differs. Ribbons keep the colour of their
// source and the period nodes are coloured by calendar progress (done/current/
// upcoming); on every stripe the already booked share is drawn opaque over the
// translucent planned hours. Project colours are sanitised and every label is
// HTML-escaped, so the markup emitted as template.HTML carries no untrusted
// content. In private mode all figures are masked; the shape of the flow stays
// visible, exactly like in the dashboard Sankey.
func goalFlowSVG(flow forecast.GoalFlow, private bool) template.HTML {
	const (
		w      = 1200.0
		h      = 520.0
		padT   = 34.0
		padB   = 14.0
		padL   = 186.0 // room for the project names on the left
		padR   = 118.0 // room for the fiscal-year label on the right
		nodeW  = 26.0
		gap    = 5.0
		minLbl = 9.0 // below this node height a label would collide with its neighbour
	)
	plotW := w - padL - padR
	plotH := h - padT - padB

	if !flow.HasData {
		return template.HTML(fmt.Sprintf( // #nosec G203 -- constant SVG shell, numeric values only
			`<svg viewBox="0 0 %g %g" class="goalflow" role="img" aria-label="Stundenfluss"><text x="%g" y="%g" font-size="13" fill="#94a3b8" text-anchor="middle">Noch keine Stunden im Fiskaljahr erfasst.</text></svg>`,
			w, h, w/2, h/2))
	}

	// One shared scale across all columns - otherwise the ribbons would not
	// match the node heights they connect.
	maxNodes := 0
	for _, st := range flow.Stages {
		if len(st) > maxNodes {
			maxNodes = len(st)
		}
	}
	barsH := plotH - float64(maxNodes-1)*gap
	if barsH < 40 {
		barsH = 40
	}
	scale := barsH / flow.Total

	type placed struct {
		node       forecast.GoalFlowNode
		stage, idx int
		x, top, ht float64
		outY, inY  float64
	}
	byID := make(map[string]*placed, maxNodes*len(flow.Stages))
	for si, st := range flow.Stages {
		x := padL + (plotW-nodeW)*float64(si)/float64(len(flow.Stages)-1)
		colH := barsH + float64(len(st)-1)*gap
		y := padT + (plotH-colH)/2
		for ni, n := range st {
			ht := scale * n.Hours
			p := &placed{node: n, stage: si, idx: ni, x: x, top: y, ht: ht}
			p.outY, p.inY = y, y
			byID[n.ID] = p
			y += ht + gap
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %g %g" class="goalflow" role="img" aria-label="Stundenfluss vom Projekt bis zum Fiskaljahr">`, w, h)

	for si, name := range forecast.GoalFlowStages {
		if si >= len(flow.Stages) {
			break
		}
		x := padL + (plotW-nodeW)*float64(si)/float64(len(flow.Stages)-1) + nodeW/2
		fmt.Fprintf(&b, `<text x="%g" y="18" font-size="11" font-weight="600" fill="#475569" text-anchor="middle">%s</text>`,
			x, template.HTMLEscapeString(name))
	}

	// Ribbons first (behind the nodes). Source-side offsets follow the target
	// order and target-side offsets the source order, so the bands do not cross
	// more than the data itself requires.
	idxOut := make([]int, 0, len(flow.Links))
	for i, l := range flow.Links {
		if byID[l.From] != nil && byID[l.To] != nil {
			idxOut = append(idxOut, i)
		}
	}
	idxIn := append([]int(nil), idxOut...)
	sort.SliceStable(idxOut, func(a, c int) bool {
		return byID[flow.Links[idxOut[a]].To].idx < byID[flow.Links[idxOut[c]].To].idx
	})
	sort.SliceStable(idxOut, func(a, c int) bool {
		return byID[flow.Links[idxOut[a]].From].idx < byID[flow.Links[idxOut[c]].From].idx
	})
	sort.SliceStable(idxIn, func(a, c int) bool {
		return byID[flow.Links[idxIn[a]].From].idx < byID[flow.Links[idxIn[c]].From].idx
	})
	sort.SliceStable(idxIn, func(a, c int) bool {
		return byID[flow.Links[idxIn[a]].To].idx < byID[flow.Links[idxIn[c]].To].idx
	})

	type slice struct{ top, bot float64 }
	target := make([]slice, len(flow.Links))
	for _, i := range idxIn {
		to := byID[flow.Links[i].To]
		ht := scale * flow.Links[i].Hours
		target[i] = slice{to.inY, to.inY + ht}
		to.inY += ht
	}
	for _, i := range idxOut {
		l := flow.Links[i]
		from, to := byID[l.From], byID[l.To]
		ht := scale * l.Hours
		a0, a1 := from.outY, from.outY+ht
		from.outY += ht
		t := target[i]
		x0, x1 := from.x+nodeW, to.x
		xc := (x0 + x1) / 2
		fmt.Fprintf(&b,
			`<path class="ribbon" d="M%g %g C%g %g %g %g %g %g L%g %g C%g %g %g %g %g %g Z" fill="%s" fill-opacity="0.42"><title>%s → %s&#10;%s h</title></path>`,
			x0, a0, xc, a0, xc, t.top, x1, t.top,
			x1, t.bot, xc, t.bot, xc, a1, x0, a1, sanitizeColor(l.Color),
			template.HTMLEscapeString(l.FromLabel), template.HTMLEscapeString(l.ToLabel),
			chartHours(l.Hours, private))
	}

	last := len(flow.Stages) - 1
	for _, st := range flow.Stages {
		for _, n := range st {
			p := byID[n.ID]
			if p == nil {
				continue
			}
			title := goalFlowTitle(n, private)
			// The base stripe carries the planned hours; the opaque overlay on top
			// of it is the share that is already booked.
			col := sanitizeColor(n.Color)
			fmt.Fprintf(&b,
				`<rect class="node" x="%g" y="%g" width="%g" height="%g" rx="2" fill="%s" fill-opacity="0.55"><title>%s</title></rect>`,
				p.x, p.top, nodeW, p.ht, col, title)
			if bh := scale * n.Booked; bh > 0 {
				if bh > p.ht {
					bh = p.ht
				}
				fmt.Fprintf(&b,
					`<rect class="node" x="%g" y="%g" width="%g" height="%g" rx="2" fill="%s"><title>%s</title></rect>`,
					p.x, p.top+p.ht-bh, nodeW, bh, col, title)
			}
			if p.ht < minLbl {
				continue
			}
			cy := p.top + p.ht/2 + 3.5
			label := template.HTMLEscapeString(fmt.Sprintf("%s · %s h", n.Label, chartHours(n.Hours, private)))
			switch {
			case p.stage == 0:
				fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="11" fill="#334155" text-anchor="end">%s</text>`,
					p.x-8, cy, label)
			case p.stage == last:
				fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="11" font-weight="600" fill="#334155">%s</text>`,
					p.x+nodeW+8, cy, label)
			case p.ht >= 12:
				// A white halo keeps the label readable on both the translucent and
				// the opaque part of the stripe.
				fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="9" font-weight="600" fill="#1e293b" stroke="#ffffff" stroke-width="2.5" paint-order="stroke" text-anchor="middle">%s</text>`,
					p.x+nodeW/2, cy-0.5, template.HTMLEscapeString(n.Label))
			}
		}
	}

	b.WriteString(`</svg>`)
	return template.HTML(b.String()) // #nosec G203 -- sanitised colours + escaped labels; other values numeric
}

// sankeyGeom holds the shared horizontal geometry of the dashboard charts so
// the Sankey and the free-time chart below it sit on the exact same time axis.
type sankeyGeom struct {
	w, padL, padR, plotW, nodeW float64
	n                           int
}

func newSankeyGeom(n int) sankeyGeom {
	const (
		w    = 1200.0
		padL = 42.0
		padR = 18.0
	)
	plotW := w - padL - padR
	return sankeyGeom{w: w, padL: padL, padR: padR, plotW: plotW, nodeW: nodeWidth(n, plotW), n: n}
}

// nodeWidth scales a bucket's stacked bar to the room it actually has. With many
// buckets the bars stay narrow so the ribbons between them dominate (the classic
// sankey look); with only a handful a fixed narrow bar would leave nearly the
// whole chart empty, and a single bucket has no ribbons at all - there the bar
// may use a good part of the width.
func nodeWidth(n int, plotW float64) float64 {
	if n <= 1 {
		return plotW * 0.45
	}
	w := plotW / float64(n) * 0.32
	if w < 12 {
		w = 12
	}
	if w > 96 {
		w = 96
	}
	return w
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

func sankeyLegend(g sankeyGeom, data forecast.SankeyData, maxRows int, private bool) ([]sankeyLegEntry, int) {
	var out []sankeyLegEntry
	x, row := g.padL, 0
	for i, p := range data.Projects {
		text := fmt.Sprintf("%s · %s h", p.Name, chartHours(data.ProjectTotals[p.ID], private))
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

// sankeyBand is the vertical extent of one project's band inside a bucket.
type sankeyBand struct{ top, bot float64 }

// splitBand divides a band into one slot per weight, stacked bottom-up so the
// slots follow the same order as the stacked project bands.
func splitBand(bd sankeyBand, weights []float64) []sankeyBand {
	total := 0.0
	for _, w := range weights {
		total += w
	}
	out := make([]sankeyBand, len(weights))
	if total <= 0 {
		return out
	}
	h := bd.bot - bd.top
	y := bd.bot
	for i, w := range weights {
		hh := h * w / total
		out[i] = sankeyBand{top: y - hh, bot: y}
		y -= hh
	}
	return out
}

// pausedProjects returns the projects that have a band in `from` but none in
// `to` (the vacation project itself excluded), together with their hours in
// bucket `hoursIdx`. Those are the projects a vacation column interrupts.
func pausedProjects(data forecast.SankeyData, from, to map[string]sankeyBand, vacID string, hoursIdx int) ([]models.Project, []float64) {
	var ps []models.Project
	var weights []float64
	for _, p := range data.Projects {
		if p.ID == vacID {
			continue
		}
		if _, ok := from[p.ID]; !ok {
			continue
		}
		if _, ok := to[p.ID]; ok {
			continue
		}
		ps = append(ps, p)
		weights = append(weights, data.Buckets[hoursIdx].Hours[p.ID])
	}
	return ps, weights
}

// sankeySVG renders the dashboard utilization flow as a dependency-free inline
// SVG. Time buckets (weeks or months) are evenly spaced columns across the full
// width; each project forms a coloured band whose height is proportional to its
// planned hours, and adjacent buckets are joined by translucent ribbons.
// Vertical dividers separate the weeks/months and every column is annotated
// with its summed planned project hours. The project legend is drawn inside the
// chart. Vacation is an ordinary band, so a vacation week visibly absorbs the
// other projects' ribbons and releases them again afterwards. Project colours
// are sanitised and project names are HTML-escaped, so the emitted markup
// (returned as template.HTML) carries no untrusted content.
//
// In private mode every figure is masked AND each column is normalised to the
// same height, so neither the labels nor the band heights reveal how many hours
// are planned or booked - only the project mix per column stays visible.
func sankeySVG(data forecast.SankeyData, private bool) template.HTML {
	const (
		h        = 376.0 // ~20 % shorter than the original 470
		headroom = 30.0  // room above the tallest column for its value labels
		axisH    = 34.0  // week/month labels below the baseline
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
	legend, legRows := sankeyLegend(g, data, 2, private)
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
	// bucketScale normalises a column to a fixed height in private mode.
	bucketScale := func(bk forecast.SankeyBucket) func(float64) float64 {
		if !private {
			return scaleY
		}
		if bk.Total <= 0 {
			return func(float64) float64 { return 0 }
		}
		return func(val float64) float64 { return plotH * 0.9 * val / bk.Total }
	}

	// Per bucket, the top/bottom Y of each project's band (bottom-aligned stack).
	bands := make([]map[string]sankeyBand, g.n)
	for i, bk := range data.Buckets {
		bands[i] = make(map[string]sankeyBand, len(bk.Hours))
		scale := bucketScale(bk)
		y := baseY
		for _, p := range data.Projects {
			hh := bk.Hours[p.ID]
			if hh <= 0 {
				continue
			}
			bh := scale(hh)
			bands[i][p.ID] = sankeyBand{top: y - bh, bot: y}
			y -= bh
		}
	}

	// The vacation project is an ordinary band, but it also acts as the hub a
	// paused project flows into and comes back out of.
	vacID, vacName := "", ""
	for _, p := range data.Projects {
		if p.IsVacation() {
			vacID, vacName = p.ID, p.Name
			break
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

	// y gridlines + hour labels (0, 50%, 100% of the scale). Private mode drops
	// the scale entirely: a labelled axis would give the column heights meaning.
	if !private {
		for _, frac := range []float64{0, 0.5, 1} {
			val := yMax * frac
			yy := baseY - scaleY(val)
			fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#eef2f7"/>`, g.padL, yy, g.padL+g.plotW, yy)
			fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" fill="#94a3b8" text-anchor="end">%g</text>`, g.padL-6, yy+3, round1(val))
		}
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
		ribbon := func(a, c sankeyBand, color, title string) {
			fmt.Fprintf(&b,
				`<path class="ribbon" d="M%g %g C%g %g %g %g %g %g L%g %g C%g %g %g %g %g %g Z" fill="%s" fill-opacity="0.3"><title>%s</title></path>`,
				x0, a.top, xc, a.top, xc, c.top, x1, c.top,
				x1, c.bot, xc, c.bot, xc, a.bot, x0, a.bot, color, title)
		}

		for _, p := range data.Projects {
			a, okA := bands[i][p.ID]
			c, okC := bands[i+1][p.ID]
			if !okA || !okC {
				continue
			}
			ribbon(a, c, sanitizeColor(p.Color), fmt.Sprintf("%s&#10;%s: %s h → %s: %s h",
				template.HTMLEscapeString(p.Name),
				template.HTMLEscapeString(data.Buckets[i].Label), chartHours(data.Buckets[i].Hours[p.ID], private),
				template.HTMLEscapeString(data.Buckets[i+1].Label), chartHours(data.Buckets[i+1].Hours[p.ID], private)))
		}

		if vacID == "" {
			continue
		}
		// A project that pauses for a vacation column must not just stop: it flows
		// into the vacation band and comes back out of it on the far side. The
		// band is split proportionally, so the whole grey stripe is fed.
		if vac, ok := bands[i+1][vacID]; ok {
			ps, weights := pausedProjects(data, bands[i], bands[i+1], vacID, i)
			for k, slot := range splitBand(vac, weights) {
				ribbon(bands[i][ps[k].ID], slot, sanitizeColor(ps[k].Color),
					fmt.Sprintf("%s&#10;%s: %s h → %s (%s)",
						template.HTMLEscapeString(ps[k].Name),
						template.HTMLEscapeString(data.Buckets[i].Label), chartHours(data.Buckets[i].Hours[ps[k].ID], private),
						template.HTMLEscapeString(vacName), template.HTMLEscapeString(data.Buckets[i+1].Label)))
			}
		}
		if vac, ok := bands[i][vacID]; ok {
			ps, weights := pausedProjects(data, bands[i+1], bands[i], vacID, i+1)
			for k, slot := range splitBand(vac, weights) {
				ribbon(slot, bands[i+1][ps[k].ID], sanitizeColor(ps[k].Color),
					fmt.Sprintf("%s&#10;%s (%s) → %s: %s h",
						template.HTMLEscapeString(ps[k].Name),
						template.HTMLEscapeString(vacName), template.HTMLEscapeString(data.Buckets[i].Label),
						template.HTMLEscapeString(data.Buckets[i+1].Label), chartHours(data.Buckets[i+1].Hours[ps[k].ID], private)))
			}
		}
	}

	// nodes (stacked project bands) + column annotations
	for i, bk := range data.Buckets {
		x := nodeX(i)
		cx := g.centerX(i)
		scale := bucketScale(bk)
		for _, p := range data.Projects {
			bd, ok := bands[i][p.ID]
			if !ok {
				continue
			}
			col := sanitizeColor(p.Color)
			rate := ""
			if bk.SpansWeeks {
				rate = "&#10;Ø " + chartHours(bk.PerWeek, private) + " h/Woche insgesamt"
			}
			fmt.Fprintf(&b,
				`<rect class="node" x="%g" y="%g" width="%g" height="%g" fill="%s" rx="1"><title>%s&#10;%s: %s h von %s h gesamt%s</title></rect>`,
				x, bd.top, g.nodeW, bd.bot-bd.top, col,
				template.HTMLEscapeString(p.Name),
				template.HTMLEscapeString(bk.Label), chartHours(bk.Hours[p.ID], private), chartHours(bk.Total, private), rate)
		}
		// summed planned hours above the stack (muted when empty)
		top := baseY - scale(bk.Total)
		fill := "#334155"
		if bk.Total == 0 {
			fill = "#cbd5e1"
			top = baseY
		}
		if bk.SpansWeeks {
			// A column spanning several weeks says little on its own, so the burn
			// rate it implies is spelled out right below the total.
			fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="11" font-weight="600" fill="%s" text-anchor="middle">%s</text>`,
				cx, top-17, fill, chartHours(bk.Total, private))
			fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="9" fill="#64748b" text-anchor="middle">Ø %s h/Wo</text>`,
				cx, top-6, chartHours(bk.PerWeek, private))
			continue
		}
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="11" font-weight="600" fill="%s" text-anchor="middle">%s</text>`,
			cx, top-6, fill, chartHours(bk.Total, private))
	}

	axisLabels(&b, g, data.Buckets, baseY+16)

	b.WriteString(`</svg>`)
	return template.HTML(b.String()) // #nosec G203 -- sanitised colours + escaped names; other values numeric/controlled
}

// freeTimeSVG renders the remaining free working time per bucket as a column
// chart on the same time axis as the Sankey above it: capacity (weekdays minus
// public holidays) minus the planned hours, vacation included.
// Free time is green, columns below the zero line mark an overbooked bucket and
// are red.
//
// In private mode the columns only carry the sign (free vs. overbooked) at a
// fixed height and all figures are dropped, so no amount of hours leaks.
func freeTimeSVG(data forecast.SankeyData, private bool) template.HTML {
	const (
		h       = 190.0
		padT    = 26.0
		padB    = 46.0
		colFree = "#16a34a"
		colOver = "#dc2626"
	)
	g := newSankeyGeom(len(data.Buckets))
	plotH := h - padT - padB
	baseY := padT + plotH

	if g.n == 0 {
		return template.HTML(fmt.Sprintf( // #nosec G203 -- constant SVG shell, numeric values only
			`<svg viewBox="0 0 %g %g" class="freetime" role="img" aria-label="Freie Kapazität"><text x="%g" y="%g" font-size="13" fill="#94a3b8" text-anchor="middle">Kein Zeitraum gewählt.</text></svg>`,
			g.w, h, g.w/2, h/2))
	}

	// value returns the plotted magnitude: the real hours, or just the direction
	// while the private mode hides the amounts.
	value := func(v float64) float64 {
		if !private {
			return v
		}
		switch {
		case v > 0:
			return 1
		case v < 0:
			return -1
		default:
			return 0
		}
	}

	maxPos, maxNeg := 0.0, 0.0
	for _, bk := range data.Buckets {
		v := value(bk.FreeHours)
		if v > maxPos {
			maxPos = v
		}
		if -v > maxNeg {
			maxNeg = -v
		}
	}
	if private {
		maxPos, maxNeg = 1, 1 // symmetric axis so the sign is all that shows
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

	// Same width as the stacked bar of the Sankey above, so both charts line up
	// on the time axis they share.
	barW := g.nodeW

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %g %g" class="freetime" role="img" aria-label="Freie Kapazität">`, g.w, h)

	// legend (top left) + zero line; laid out by estimated text width so a longer
	// label can never run into the next swatch
	lx := g.padL
	for _, l := range []struct{ color, text string }{{colFree, "Freie Kapazität"}, {colOver, "Überbucht"}} {
		fmt.Fprintf(&b, `<rect x="%g" y="4" width="9" height="9" rx="2" fill="%s"/>`, lx, l.color)
		fmt.Fprintf(&b, `<text x="%g" y="13" font-size="11" fill="#475569">%s</text>`, lx+13, l.text)
		lx += 26 + estTextWidth(l.text, 11)
	}
	fmt.Fprintf(&b, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#cbd5e1"/>`, g.padL, zeroY, g.padL+g.plotW, zeroY)
	if !private {
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="10" fill="#94a3b8" text-anchor="end">0</text>`, g.padL-6, zeroY+3)
	}

	for i, bk := range data.Buckets {
		cx := g.centerX(i)
		v := value(bk.FreeHours)
		bh := scale(v)
		if bh < 0 {
			bh = -bh
		}
		y, col, labelY := zeroY-bh, colFree, zeroY-bh-5
		switch {
		case v < 0:
			y, col, labelY = zeroY, colOver, zeroY+bh+12
		case v == 0:
			bh, y, col, labelY = 2, zeroY-1, "#e2e8f0", zeroY-6
		}
		if private {
			fmt.Fprintf(&b, `<rect x="%g" y="%g" width="%g" height="%g" rx="2" fill="%s"><title>%s</title></rect>`,
				cx-barW/2, y, barW, bh, col, template.HTMLEscapeString(bk.Label))
			continue
		}
		fmt.Fprintf(&b, `<rect x="%g" y="%g" width="%g" height="%g" rx="2" fill="%s"><title>%s · %s h frei (Kapazität %s h, geplant %s h)</title></rect>`,
			cx-barW/2, y, barW, bh, col,
			template.HTMLEscapeString(bk.Label), formatHours(v), formatHours(bk.CapacityHours), formatHours(bk.Total))
		textFill := "#166534"
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
