package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/daknoblo/forecast-tool/internal/api"
	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/storage"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// AppName is the central application name shown in the header and footer.
const AppName = "Forecast Tool"

// Server wires storage, templates and HTTP routing together.
type Server struct {
	store  *storage.Store
	logger *slog.Logger

	// Two fully parsed template sets: the public one and the private
	// ("presentation") one whose figure-formatting functions mask every value.
	// They are cloned once at startup instead of per request, because cloning a
	// template set is far more expensive than rendering a page.
	tpl        *template.Template
	tplPrivate *template.Template

	staticFS http.Handler
}

// NewServer parses templates and returns a ready-to-mount handler. The logger is
// used for operational logging (e.g. AI endpoint usage); if nil, slog.Default()
// is used.
func NewServer(store *storage.Store, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	funcs := template.FuncMap{
		"hours":    formatHours,
		"hoursRaw": formatHours,
		"appName":  func() string { return AppName },
		"asset":    assetURL,
		"pct":      func(f float64) string { return formatHours(f) + " %" },
		"cellName": func(projectID, date string) string {
			return "h_" + projectID + "_" + date
		},
		"cellHours": func(cell forecast.DayCell, projectID string) float64 {
			return cell.Hours[projectID]
		},
		"weekTotal": func(totals map[string]float64, projectID string) float64 {
			return totals[projectID]
		},
		"bookable": func(p models.Project, date string) bool {
			return p.Bookable(date)
		},
		"add":      func(a, b int) int { return a + b },
		"barWidth": barWidth,
	}
	base, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	// Clone before either set is executed; a template set can no longer be
	// cloned once it has run.
	priv, err := base.Clone()
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	return &Server{
		store:      store,
		logger:     logger,
		tpl:        base.Funcs(privacyFuncs(false)),
		tplPrivate: priv.Funcs(privacyFuncs(true)),
		staticFS:   http.StripPrefix("/static/", cacheForever(http.FileServer(http.FS(sub)))),
	}, nil
}

// Handler builds the HTTP routing mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", s.staticFS)

	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /week", s.handleWeekRedirect)
	mux.HandleFunc("GET /week/{week}", s.handleWeek)
	mux.HandleFunc("POST /week/cells", s.handleWeekCells)
	mux.HandleFunc("POST /week/{week}", s.handleWeekSave)
	mux.HandleFunc("GET /projects", s.handleProjects)
	mux.HandleFunc("POST /projects", s.handleProjectCreate)
	mux.HandleFunc("POST /projects/{id}/update", s.handleProjectUpdate)
	mux.HandleFunc("POST /projects/{id}/delete", s.handleProjectDelete)
	mux.HandleFunc("GET /goal", s.handleGoal)
	mux.HandleFunc("POST /goal/chat", s.handleGoalChat)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings", s.handleSettingsSave)
	mux.HandleFunc("GET /export", s.handleExport)
	mux.HandleFunc("POST /fy", s.handleSetActiveFY)
	mux.HandleFunc("POST /private", s.handlePrivateToggle)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	root := http.NewServeMux()
	// The unauthenticated HTML UI additionally requires state-changing requests
	// to originate from this site (CSRF defence).
	root.Handle("/", requireSameOrigin(mux))
	// JSON API for external clients (token-protected; the HTML UI stays open).
	// It is mounted outside the same-origin guard on purpose: it authenticates
	// with a bearer token, which a cross-site form post can never supply.
	root.Handle("/api/", api.New(s.store, s.logger))

	return securityHeaders(root)
}

func (s *Server) calendar(d models.Data) *holidays.Calendar {
	return holidays.Get(d.Settings.Year, d.Settings.FederalState)
}

// render executes a template with the request's private-mode settings applied.
// It picks one of the two template sets prepared at startup and renders into a
// buffer first, so a template error cannot leave a half-written page behind.
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	private := isPrivate(r)
	if m, ok := data.(map[string]any); ok {
		m["Private"] = private
	}
	tpl := s.tpl
	if private {
		tpl = s.tplPrivate
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.logger.Error("template render failed", "template", name, "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = w.Write(buf.Bytes())
}

// --- Dashboard ---

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	private := isPrivate(r)
	d := maskIfPrivate(s.store.Snapshot(), r)
	cal := s.calendar(d)
	// BuildYearSummary needs every fiscal year's projects to resolve the
	// per-assignment carry-over; it scopes the summary to the active FY itself.
	ys := forecast.BuildYearSummary(d, cal)
	// The "open until goal" tile measures against the fiscal year's hour goal,
	// not against the summed project budgets.
	goal := forecast.BuildGoalSummary(d, cal)
	d.Projects = models.ProjectsForFY(d.Projects, d.Settings.Year)
	projects := forecast.SortedProjects(d.Projects)
	fyStart, fyEnd := forecast.FiscalYear(d.Settings.Year, d.Settings.FiscalYearStartMonth)
	sankeyOffset, _ := strconv.Atoi(trim(r.URL.Query().Get("soff")))
	sankey := forecast.BuildSankey(d, cal, r.URL.Query().Get("sankey"), sankeyOffset)
	// Extra context for the KPI tooltips.
	curWeek := forecast.CurrentFYWeek(d.Settings.Year, d.Settings.FiscalYearStartMonth)
	curWeekRange := ""
	if curWeek >= 1 && curWeek <= len(ys.WeekTotals) {
		curWeekRange = ys.WeekTotals[curWeek-1].RangeLabel
	}
	s.render(w, r, "dashboard.html", map[string]any{
		"Active":         "dashboard",
		"Wide":           true,
		"Settings":       d.Settings,
		"FYYears":        fyYears(d),
		"Summary":        ys,
		"Goal":           goal,
		"WeekToDate":     forecast.BuildWeekToDate(d),
		"Projects":       projects,
		"ActiveProjects": len(activeProjects(projects)),
		"CurrentWeek":    curWeek,
		"CurrentRange":   curWeekRange,
		"FYWeekCount":    len(ys.WeekTotals),
		"FYStart":        fyStart.Format("02.01.2006"),
		"FYEnd":          fyEnd.Format("02.01.2006"),
		"Sankey":         sankey,
		"SankeyRanges":   forecast.SankeyRanges,
		"SankeySVG":      sankeySVG(sankey, private),
		"FreeTimeSVG":    freeTimeSVG(sankey, private),
	})
}

// --- Week views ---

func (s *Server) handleWeekRedirect(w http.ResponseWriter, r *http.Request) {
	d := s.store.Snapshot()
	http.Redirect(w, r, "/week/"+strconv.Itoa(forecast.CurrentFYWeek(d.Settings.Year, d.Settings.FiscalYearStartMonth)), http.StatusFound)
}

func (s *Server) handleWeek(w http.ResponseWriter, r *http.Request) {
	d := maskIfPrivate(s.store.Snapshot(), r)
	cal := s.calendar(d)
	// Built before narrowing the projects: the summary resolves the
	// per-assignment carry-over across fiscal years itself.
	ys := forecast.BuildYearSummary(d, cal)
	d.Projects = models.ProjectsForFY(d.Projects, d.Settings.Year)
	start := clampWeek(r.PathValue("week"), d.Settings)
	weeks := spanWeeks(r)
	sv := forecast.BuildSpan(d, cal, start, weeks)
	projects := forecast.SortedProjects(activeProjects(d.Projects))
	spanStart, spanEnd := "", ""
	if len(sv.Days) > 0 {
		spanStart = sv.Days[0].Date
		spanEnd = sv.Days[len(sv.Days)-1].Date
	}
	burn := forecast.BuildSpanBurn(ys.Projects, spanStart, spanEnd)
	budgetLeft := map[string]float64{}
	for _, p := range ys.Projects {
		// The remaining budget is fed into the live JS totals; in private mode it
		// must not leak the real figure through the data attribute.
		if isPrivate(r) {
			budgetLeft[p.Project.ID] = 0
			continue
		}
		budgetLeft[p.Project.ID] = round1(p.Remaining)
	}
	s.render(w, r, "week.html", map[string]any{
		"Active":      "week",
		"Wide":        true,
		"Settings":    d.Settings,
		"FYYears":     fyYears(d),
		"Span":        sv,
		"Burn":        burn,
		"MaxWeek":     sv.MaxWeek,
		"WeekChoices": []int{1, 2, 3, 4, 6, 8},
		"Projects":    projects,
		"AllProjects": forecast.SortedProjects(d.Projects),
		"BudgetLeft":  budgetLeft,
	})
}

func (s *Server) handleWeekSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	d := s.store.Snapshot()
	start := clampWeek(r.PathValue("week"), d.Settings)
	weeks := spanWeeks(r)
	max := forecast.FYWeeks(d.Settings.Year, d.Settings.FiscalYearStartMonth)
	if weeks > max {
		weeks = max
	}
	if start+weeks-1 > max {
		start = max - weeks + 1
		if start < 1 {
			start = 1
		}
	}

	// Collect every Mon-Fri date across the visible span of weeks.
	weekDates := map[string]bool{}
	for wi := 0; wi < weeks; wi++ {
		monday := forecast.FYWeekMonday(d.Settings.Year, d.Settings.FiscalYearStartMonth, start+wi)
		for i := 0; i < 5; i++ {
			weekDates[monday.AddDate(0, 0, i).Format("2006-01-02")] = true
		}
	}

	type key struct{ date, project string }
	newHours := map[key]float64{}
	for name, vals := range r.Form {
		if len(name) < 3 || name[:2] != "h_" {
			continue
		}
		// h_{projectID}_{YYYY-MM-DD}
		rest := name[2:]
		if len(rest) < 11 {
			continue
		}
		date := rest[len(rest)-10:]
		projectID := rest[:len(rest)-11]
		if !weekDates[date] {
			continue
		}
		h, err := strconv.ParseFloat(normalizeNum(vals[0]), 64)
		if err != nil || h < 0 {
			continue
		}
		newHours[key{date, projectID}] = h
	}

	err := s.store.Update(func(data *models.Data) error {
		// Drop existing entries for this week, then re-add the non-zero values.
		kept := data.Entries[:0]
		for _, e := range data.Entries {
			if !weekDates[e.Date] {
				kept = append(kept, e)
			}
		}
		data.Entries = append([]models.Entry(nil), kept...)
		// stable order
		keys := make([]key, 0, len(newHours))
		for k := range newHours {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].date != keys[j].date {
				return keys[i].date < keys[j].date
			}
			return keys[i].project < keys[j].project
		})
		for _, k := range keys {
			if newHours[k] > 0 {
				data.Entries = append(data.Entries, models.Entry{
					Date: k.date, ProjectID: k.project, Hours: newHours[k],
				})
			}
		}
		return nil
	})
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/week/%d?weeks=%d", start, weeks), http.StatusSeeOther) // #nosec G710 -- path built from integers only, not user-controlled
}

// cellIn is one auto-saved forecast cell from the week grid.
type cellIn struct {
	Date      string  `json:"date"`
	ProjectID string  `json:"projectId"`
	Hours     float64 `json:"hours"`
}

// handleWeekCells upserts a small batch of forecast cells from the week grid's
// auto-save (so the page never reloads while the user types). Each cell is keyed
// by (date, projectId); hours <= 0 clears the entry. Cells for unknown projects
// are skipped and counted, never failing the batch. A date outside the project's
// booking window is accepted - the window only drives the visual hint and the
// "outside the window" warning. Writes go through store.Mutate (normalize +
// validate + persist).
func (s *Server) handleWeekCells(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var in struct {
		Cells []cellIn `json:"cells"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "ung\u00fcltige Anfrage")
		return
	}
	if len(in.Cells) == 0 || len(in.Cells) > 500 {
		writeJSONError(w, http.StatusBadRequest, "keine oder zu viele Zellen")
		return
	}
	type key struct{ date, pid string }
	want := make(map[key]float64, len(in.Cells))
	for _, c := range in.Cells {
		date := validISODate(c.Date)
		pid := trim(c.ProjectID)
		if date == "" || pid == "" || c.Hours < 0 {
			writeJSONError(w, http.StatusBadRequest, "ung\u00fcltige Zelle")
			return
		}
		want[key{date, pid}] = c.Hours
	}
	skipped := 0
	err := s.store.Mutate(func(d *models.Data) error {
		projByID := make(map[string]models.Project, len(d.Projects))
		for _, p := range d.Projects {
			projByID[p.ID] = p
		}
		// Drop the entries we are replacing, then re-add the non-zero values.
		kept := d.Entries[:0]
		for _, e := range d.Entries {
			if _, ok := want[key{e.Date, e.ProjectID}]; ok {
				continue
			}
			kept = append(kept, e)
		}
		d.Entries = append([]models.Entry(nil), kept...)
		ks := make([]key, 0, len(want))
		for k := range want {
			ks = append(ks, k)
		}
		sort.Slice(ks, func(i, j int) bool {
			if ks[i].date != ks[j].date {
				return ks[i].date < ks[j].date
			}
			return ks[i].pid < ks[j].pid
		})
		for _, k := range ks {
			if _, ok := projByID[k.pid]; !ok {
				skipped++
				continue
			}
			if want[k] > 0 {
				d.Entries = append(d.Entries, models.Entry{Date: k.date, ProjectID: k.pid, Hours: want[k]})
			}
		}
		return nil
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Speichern fehlgeschlagen")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = fmt.Fprintf(w, `{"ok":true,"skipped":%d}`, skipped)
}

// writeJSONError writes a minimal JSON error object with the given status code.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]string{"error": msg})
	_, _ = w.Write(b)
}

// --- Projects ---

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	private := isPrivate(r)
	d := maskIfPrivate(s.store.Snapshot(), r)
	// Pass every fiscal year's projects so the per-assignment carry-over is
	// resolved; the summary is scoped to the active FY inside.
	ys := forecast.BuildYearSummary(d, s.calendar(d))

	type projView struct {
		Summary  forecast.ProjectSummary
		Burndown template.HTML
	}
	var views []projView
	for _, ps := range ys.Projects {
		// The burn-down starts at the budget still available in this fiscal year.
		pts := forecast.BuildBurndown(d, ps.Project.ID, ps.StartDate, ps.EndDate, ps.AvailableBudget)
		views = append(views, projView{
			Summary:  ps,
			Burndown: burndownSVG(pts, ps.AvailableBudget, ps.Project.Color, private),
		})
	}
	sort.Slice(views, func(i, j int) bool {
		return views[i].Summary.Project.Name < views[j].Summary.Project.Name
	})

	s.render(w, r, "projects.html", map[string]any{
		"Active":   "projects",
		"Settings": d.Settings,
		"FYYears":  fyYears(d),
		"Views":    views,
	})
}

func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := capLen(trim(r.FormValue("name")), 200)
	assignmentID := capLen(trim(r.FormValue("assignmentId")), 100)
	if name == "" || assignmentID == "" {
		http.Redirect(w, r, "/projects", http.StatusSeeOther)
		return
	}
	budget, _ := strconv.ParseFloat(normalizeNum(r.FormValue("budget")), 64)
	if budget < 0 {
		budget = 0
	}
	startDate := validISODate(r.FormValue("startDate"))
	endDate := validISODate(r.FormValue("endDate"))
	_ = s.store.Update(func(d *models.Data) error {
		used := make([]string, 0, len(d.Projects))
		for _, p := range d.Projects {
			used = append(used, p.Color)
		}
		d.Projects = append(d.Projects, models.Project{
			ID:           newID(),
			AssignmentID: assignmentID,
			Name:         name,
			BudgetHours:  budget,
			Color:        models.RandomColor(used),
			Active:       true,
			FiscalYear:   d.Settings.Year,
			StartDate:    startDate,
			EndDate:      endDate,
		})
		return nil
	})
	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}

func (s *Server) handleProjectUpdate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	name := capLen(trim(r.FormValue("name")), 200)
	assignmentID := capLen(trim(r.FormValue("assignmentId")), 100)
	budget, _ := strconv.ParseFloat(normalizeNum(r.FormValue("budget")), 64)
	if budget < 0 {
		budget = 0
	}
	color := trim(r.FormValue("color"))
	if color != "" && !models.IsHexColor(color) {
		color = ""
	}
	active := r.FormValue("active") != ""
	startDate := validISODate(r.FormValue("startDate"))
	endDate := validISODate(r.FormValue("endDate"))
	_ = s.store.Update(func(d *models.Data) error {
		for i := range d.Projects {
			if d.Projects[i].ID == id {
				if name != "" {
					d.Projects[i].Name = name
				}
				if assignmentID != "" {
					d.Projects[i].AssignmentID = assignmentID
				}
				// The vacation project's budget is derived from the vacation days
				// in the FY settings, so it is not editable here.
				if !d.Projects[i].IsVacation() {
					d.Projects[i].BudgetHours = budget
				}
				if color != "" {
					d.Projects[i].Color = color
				}
				d.Projects[i].Active = active
				d.Projects[i].StartDate = startDate
				d.Projects[i].EndDate = endDate
			}
		}
		return nil
	})
	if isAutoSave(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}

func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_ = s.store.Update(func(d *models.Data) error {
		out := make([]models.Project, 0, len(d.Projects))
		removed := false
		for _, p := range d.Projects {
			// The vacation project is auto-managed and cannot be deleted.
			if p.ID == id && !p.IsVacation() {
				removed = true
				continue
			}
			out = append(out, p)
		}
		if !removed {
			return nil
		}
		d.Projects = out
		// also drop entries of that project
		entries := make([]models.Entry, 0, len(d.Entries))
		for _, e := range d.Entries {
			if e.ProjectID != id {
				entries = append(entries, e)
			}
		}
		d.Entries = entries
		return nil
	})
	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}

// --- Goal (fiscal year target) ---

func (s *Server) handleGoal(w http.ResponseWriter, r *http.Request) {
	private := isPrivate(r)
	d := maskIfPrivate(s.store.Snapshot(), r)
	cal := s.calendar(d)
	gs := forecast.BuildGoalSummary(d, cal)
	ys := forecast.BuildYearSummary(d, cal)

	// Cumulative projected hours per month drive the progress charts for the
	// whole FY, each half-year and each quarter.
	var fyChart, h1Chart, h2Chart template.HTML
	quarterCharts := make([]template.HTML, 4)
	if len(gs.Months) == 12 {
		labels := make([]string, 12)
		proj := make([]float64, 12)
		for i, m := range gs.Months {
			labels[i] = m.Label
			proj[i] = m.Projected
		}
		done := forecast.FYMonthsDone(d.Settings.Year, d.Settings.FiscalYearStartMonth)
		clamp := func(v, max int) int {
			if v < 0 {
				return 0
			}
			if v > max {
				return max
			}
			return v
		}
		fyChart = progressSVG(labels, cumulative(proj), gs.TargetHours, done, private)
		h1Chart = progressSVG(labels[:6], cumulative(proj[:6]), round1(gs.TargetHours/2), clamp(done, 6), private)
		h2Chart = progressSVG(labels[6:], cumulative(proj[6:]), round1(gs.TargetHours/2), clamp(done-6, 6), private)
		for q := 0; q < 4; q++ {
			from, to := q*3, q*3+3
			quarterCharts[q] = progressSVG(labels[from:to], cumulative(proj[from:to]),
				round1(gs.TargetHours/4), clamp(done-from, 3), private)
		}
	}

	promptsJSON, err := json.Marshal(func() []string {
		out := make([]string, len(chatPresets))
		for i, p := range chatPresets {
			out[i] = p.Prompt
		}
		return out
	}())
	if err != nil {
		promptsJSON = []byte("[]")
	}

	s.render(w, r, "goal.html", map[string]any{
		"Active":          "goal",
		"Settings":        d.Settings,
		"FYYears":         fyYears(d),
		"Goal":            gs,
		"WeekTotals":      ys.WeekTotals,
		"FYChart":         fyChart,
		"H1Chart":         h1Chart,
		"H2Chart":         h2Chart,
		"QuarterCharts":   quarterCharts,
		"FlowSVG":         goalFlowSVG(forecast.BuildGoalFlow(d), private),
		"ChatPresets":     chatPresets,
		"ChatPromptsJSON": template.JS(promptsJSON), // #nosec G203 -- JSON-encoded constants, no user input
		"AIConfigured":    aiConfigured(effectiveAI(d.Settings.AI)),
		"AIKeyEnv":        aiAPIKeyEnv,
	})
}

// --- Settings ---

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	d := s.store.Snapshot()
	viewYear := d.Settings.Year
	if q := trim(r.URL.Query().Get("year")); q != "" {
		if y, err := strconv.Atoi(q); err == nil && models.ValidYear(y) {
			viewYear = y
		}
	}
	fy := d.FYFor(viewYear)
	fyStart, fyEnd := forecast.FiscalYear(viewYear, d.Settings.FiscalYearStartMonth)
	h2Start := fyStart.AddDate(0, 6, 0)
	h1End := h2Start.AddDate(0, 0, -1)
	s.render(w, r, "settings.html", map[string]any{
		"Active":       "settings",
		"Settings":     d.Settings,
		"FYYears":      fyYears(d),
		"States":       holidays.States,
		"Months":       monthOptions,
		"DataPath":     s.store.Path(),
		"DataSize":     formatBytes(s.store.FileSize()),
		"ViewYear":     viewYear,
		"PrevYear":     viewYear - 1,
		"NextYear":     viewYear + 1,
		"IsActive":     viewYear == d.Settings.Year,
		"FY":           fy,
		"FYStart":      fyStart.Format("02.01.2006"),
		"FYEnd":        fyEnd.Format("02.01.2006"),
		"H1Label":      halfLabel(fyStart, h1End),
		"H2Label":      halfLabel(h2Start, fyEnd),
		"AIKeyEnv":     aiAPIKeyEnv,
		"AIKeySet":     trim(os.Getenv(aiAPIKeyEnv)) != "",
		"AIKeyInStore": trim(d.Settings.AI.APIKey) != "",
		"APIReadEnv":   api.ReadTokenEnv,
		"APIReadSet":   trim(os.Getenv(api.ReadTokenEnv)) != "",
		"APIWriteEnv":  api.WriteTokenEnv,
		"APIWriteSet":  trim(os.Getenv(api.WriteTokenEnv)) != "",
	})
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if trim(r.FormValue("section")) == "ai" {
		endpoint := trim(r.FormValue("aiEndpoint"))
		deployment := trim(r.FormValue("aiDeployment"))
		apiVersion := trim(r.FormValue("aiApiVersion"))
		_ = s.store.Update(func(d *models.Data) error {
			d.Settings.AI.Endpoint = endpoint
			d.Settings.AI.Deployment = deployment
			d.Settings.AI.APIVersion = apiVersion
			// The secret key is provided via FORECAST_AI_API_KEY and must never be
			// stored in the data file; clear any legacy value on save.
			d.Settings.AI.APIKey = ""
			return nil
		})
		s.settingsSaved(w, r)
		return
	}
	if trim(r.FormValue("section")) == "utilization" {
		minH, minErr := strconv.ParseFloat(normalizeNum(r.FormValue("utilMin")), 64)
		optH, optErr := strconv.ParseFloat(normalizeNum(r.FormValue("utilOptimal")), 64)
		overH, overErr := strconv.ParseFloat(normalizeNum(r.FormValue("utilOver")), 64)
		minLabel := capLen(trim(r.FormValue("utilMinLabel")), 60)
		optLabel := capLen(trim(r.FormValue("utilOptimalLabel")), 60)
		highLabel := capLen(trim(r.FormValue("utilHighLabel")), 60)
		overLabel := capLen(trim(r.FormValue("utilOverLabel")), 60)
		_ = s.store.Update(func(d *models.Data) error {
			if minErr == nil && minH >= 0 {
				d.Settings.Utilization.MinHours = minH
			}
			if optErr == nil && optH >= 0 {
				d.Settings.Utilization.OptimalHours = optH
			}
			if overErr == nil && overH >= 0 {
				d.Settings.Utilization.OverHours = overH
			}
			if minLabel != "" {
				d.Settings.Utilization.MinLabel = minLabel
			}
			if optLabel != "" {
				d.Settings.Utilization.OptimalLabel = optLabel
			}
			if highLabel != "" {
				d.Settings.Utilization.HighLabel = highLabel
			}
			if overLabel != "" {
				d.Settings.Utilization.OverLabel = overLabel
			}
			return nil
		})
		s.settingsSaved(w, r)
		return
	}
	year, _ := strconv.Atoi(trim(r.FormValue("year")))
	state := trim(r.FormValue("state"))
	weekly, _ := strconv.ParseFloat(normalizeNum(r.FormValue("weekly")), 64)
	fyStartMonth, fyMonthErr := strconv.Atoi(trim(r.FormValue("fyStartMonth")))
	fyTarget, fyErr := strconv.ParseFloat(normalizeNum(r.FormValue("fyTarget")), 64)
	vacH1, vacH1Err := strconv.Atoi(trim(r.FormValue("vacationH1")))
	vacH2, vacH2Err := strconv.Atoi(trim(r.FormValue("vacationH2")))
	stdHours, stdErr := strconv.ParseFloat(normalizeNum(r.FormValue("standardTaskHours")), 64)
	_ = s.store.Update(func(d *models.Data) error {
		// The page can edit a fiscal year other than the active one (?year=).
		// Only the per-FY block is written to that year; switching the ACTIVE
		// fiscal year stays the job of the header dropdown (POST /fy).
		target := d.Settings.Year
		if models.ValidYear(year) {
			target = year
		}
		if state != "" {
			d.Settings.FederalState = state
		}
		if weekly > 0 {
			d.Settings.WeeklyTargetHours = weekly
		}
		if fyMonthErr == nil && fyStartMonth >= 1 && fyStartMonth <= 12 {
			d.Settings.FiscalYearStartMonth = fyStartMonth
		}
		if d.FiscalYears == nil {
			d.FiscalYears = map[int]models.FiscalYearSettings{}
		}
		fy := d.FYFor(target)
		if fyErr == nil && fyTarget >= 0 {
			fy.TargetHours = fyTarget
		}
		if vacH1Err == nil && vacH1 >= 0 && vacH1 <= 366 {
			fy.VacationDaysH1 = vacH1
		}
		if vacH2Err == nil && vacH2 >= 0 && vacH2 <= 366 {
			fy.VacationDaysH2 = vacH2
		}
		if stdErr == nil && stdHours >= 0 {
			fy.StandardTaskHours = stdHours
		}
		d.FiscalYears[target] = fy
		// Keep the vacation project's budget in sync with the vacation days.
		models.EnsureVacationProject(d, target)
		return nil
	})
	s.settingsSaved(w, r)
}

// settingsSaved ends a settings write: the background auto-save just needs a
// status code, a plain form submit is sent back to the settings page.
func (s *Server) settingsSaved(w http.ResponseWriter, r *http.Request) {
	if isAutoSave(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// handleExport streams the current data document as a JSON file download so the
// user can back up or move their data out of the application.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	b, err := s.store.Marshal()
	if err != nil {
		http.Error(w, "export failed", http.StatusInternalServerError)
		return
	}
	filename := "forecast-export-" + time.Now().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}

// aiAPIKeyEnv is the NAME of the environment variable that supplies the secret
// AI API key. The key itself is never stored in code or the data file.
const aiAPIKeyEnv = "FORECAST_AI_API_KEY" // #nosec G101 -- env var name, not a credential

// aiConfigured reports whether the minimum AI endpoint settings are present.
func aiConfigured(a models.AISettings) bool {
	return trim(a.Endpoint) != "" && trim(a.Deployment) != "" && trim(a.APIKey) != ""
}

// effectiveAI overlays the API key from the environment so the secret never has
// to live in the data file. A stored (legacy) key is used only as a fallback.
func effectiveAI(a models.AISettings) models.AISettings {
	if k := trim(os.Getenv(aiAPIKeyEnv)); k != "" {
		a.APIKey = k
	}
	return a
}

// handleSetActiveFY switches the globally active fiscal year (used by the
// dropdown in the header) and returns to the page the user came from.
func (s *Server) handleSetActiveFY(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if year, err := strconv.Atoi(trim(r.FormValue("year"))); err == nil && models.ValidYear(year) {
		_ = s.store.Update(func(d *models.Data) error {
			d.Settings.Year = year
			// Make sure the vacation project exists for the newly active FY.
			models.EnsureVacationProject(d, year)
			return nil
		})
	}
	// Redirect back to the page the user came from, but only to a same-origin
	// path so a crafted Referer header cannot cause an open redirect.
	http.Redirect(w, r, refererPath(r), http.StatusSeeOther) // #nosec G710 -- same-origin path/query only
}

// --- helpers ---

// refererPath returns the local path (with query) of the request's Referer so
// the user is sent back to the page they came from, never to an external
// origin. Scheme and host are discarded, which prevents an open redirect.
func refererPath(r *http.Request) string {
	ref := trim(r.Header.Get("Referer"))
	if ref == "" {
		return "/"
	}
	u, err := url.Parse(ref)
	if err != nil || u.Path == "" {
		return "/"
	}
	dest := u.Path
	if u.RawQuery != "" {
		dest += "?" + u.RawQuery
	}
	return dest
}

// halfLabel formats a half-year range like "Juli 2026 – Dezember 2026".
func halfLabel(start, end time.Time) string {
	return fmt.Sprintf("%s %d – %s %d",
		monthName(int(start.Month())), start.Year(),
		monthName(int(end.Month())), end.Year())
}

// monthName returns the German month name for 1..12.
func monthName(m int) string {
	if m < 1 || m > 12 {
		return ""
	}
	return monthOptions[m-1].Name
}

// monthOption is a selectable month for the fiscal-year start dropdown.
type monthOption struct {
	Value int
	Name  string
}

var monthOptions = []monthOption{
	{1, "Januar"}, {2, "Februar"}, {3, "März"}, {4, "April"},
	{5, "Mai"}, {6, "Juni"}, {7, "Juli"}, {8, "August"},
	{9, "September"}, {10, "Oktober"}, {11, "November"}, {12, "Dezember"},
}

func activeProjects(ps []models.Project) []models.Project {
	out := make([]models.Project, 0, len(ps))
	for _, p := range ps {
		if p.Active {
			out = append(out, p)
		}
	}
	return out
}

func clampWeek(raw string, st models.Settings) int {
	max := forecast.FYWeeks(st.Year, st.FiscalYearStartMonth)
	w, err := strconv.Atoi(raw)
	if err != nil {
		return forecast.CurrentFYWeek(st.Year, st.FiscalYearStartMonth)
	}
	if w < 1 {
		w = 1
	}
	if w > max {
		w = max
	}
	return w
}

// spanWeeks parses the number of consecutive weeks to display from the request
// query (?weeks=N), clamped to a sane range. Defaults to 1 when absent.
func spanWeeks(r *http.Request) int {
	n, err := strconv.Atoi(trim(r.URL.Query().Get("weeks")))
	if err != nil || n < 1 {
		return 1
	}
	if n > 52 {
		n = 52
	}
	return n
}

func newID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

// fyYears returns the sorted list of fiscal years offered in the header
// dropdown: every configured year plus a small range around the active one.
func fyYears(d models.Data) []int {
	set := map[int]bool{}
	for y := range d.FiscalYears {
		set[y] = true
	}
	for y := d.Settings.Year - 1; y <= d.Settings.Year+2; y++ {
		set[y] = true
	}
	years := make([]int, 0, len(set))
	for y := range set {
		years = append(years, y)
	}
	sort.Ints(years)
	return years
}

// formatBytes renders a byte count as a human-readable string (B/KB/MB).
func formatBytes(n int64) string {
	switch {
	case n < 1024:
		return strconv.FormatInt(n, 10) + " B"
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
