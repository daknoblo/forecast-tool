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

// Version is the release this binary was built from, shown in the footer. The
// container build stamps it via
// `-ldflags "-X github.com/daknoblo/forecast-tool/internal/web.Version=v1.2.0"`;
// a plain `go build` or `go run` leaves it at "dev".
var Version = "dev"

// Server wires storage, templates and HTTP routing together.
type Server struct {
	store   *storage.Store
	logger  *slog.Logger
	foundry *foundryState
	monthAI monthAIState

	tpl *template.Template

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
		"hours":            formatHours,
		"monthExplanation": renderMonthExplanation,
		// hoursRaw marks a figure that goes into a form field rather than onto the
		// page; same formatting, different intent.
		"hoursRaw": formatHours,
		"appName":  func() string { return AppName },
		"version":  func() string { return Version },
		"asset":    assetURL,
		"pct":      func(f float64) string { return formatHours(f) + " %" },
		"barWidth": barWidth,
	}
	tpl, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	return &Server{
		store:    store,
		logger:   logger,
		foundry:  newFoundryState(),
		tpl:      tpl,
		staticFS: http.StripPrefix("/static/", cacheForever(http.FileServer(http.FS(sub)))),
	}, nil
}

// Handler builds the HTTP routing mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", s.staticFS)

	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /month", s.handleMonth)
	mux.HandleFunc("POST /month/generate", s.handleMonthGenerate)
	mux.HandleFunc("POST /month/save", s.handleMonthSave)
	mux.HandleFunc("POST /month/prompt", s.handleMonthPrompt)
	mux.HandleFunc("GET /projects", s.handleProjects)
	mux.HandleFunc("POST /projects", s.handleProjectCreate)
	mux.HandleFunc("POST /projects/{id}/update", s.handleProjectUpdate)
	mux.HandleFunc("POST /projects/{id}/active", s.handleProjectActive)
	mux.HandleFunc("POST /projects/{id}/delete", s.handleProjectDelete)
	mux.HandleFunc("GET /goal", s.handleGoal)
	mux.HandleFunc("POST /goal/chat", s.handleGoalChat)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings", s.handleSettingsSave)
	mux.HandleFunc("POST /settings/ai/refresh", s.handleFoundryRefresh)
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

// render executes a template with the request's private-mode flag applied. It
// renders into a buffer first, so a template error cannot leave a half-written
// page behind.
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	if m, ok := data.(map[string]any); ok {
		m["Private"] = isPrivate(r)
	}
	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, name, data); err != nil {
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
	d := s.viewData(r)
	cal := s.calendar(d)
	// BuildYearSummary needs every fiscal year's projects to resolve the
	// per-assignment carry-over; it scopes the summary to the active FY itself.
	ys := forecast.BuildYearSummary(d, cal)
	// The "open until goal" tile measures against the fiscal year's hour goal,
	// not against the summed project budgets.
	goal := forecast.BuildGoalSummary(d, cal)
	// Built before the projects are narrowed to the active FY: the rolling window
	// is anchored on today and may reach into another fiscal year.
	workload := forecast.BuildWorkload(d, forecast.WorkloadTileMonths)
	workloadPlan := forecast.BuildWorkloadPlan(d, forecast.WorkloadTileMonths)
	d.Projects = models.ProjectsForFY(d.Projects, d.Settings.Year)
	projects := forecast.SortedProjects(d.Projects)
	fyStart, fyEnd := forecast.FiscalYear(d.Settings.Year, d.Settings.FiscalYearStartMonth)
	sankeyOffset, _ := strconv.Atoi(trim(r.URL.Query().Get("soff")))
	rangeKey := r.URL.Query().Get("sankey")
	if !models.ValidDashboardRange(rangeKey) {
		rangeKey = d.Settings.DashboardRange
	}
	sankey := forecast.BuildSankey(d, cal, rangeKey, sankeyOffset)
	// Extra context for the KPI tooltips.
	curWeek := forecast.CurrentFYWeek(d.Settings.Year, d.Settings.FiscalYearStartMonth)
	curWeekRange := ""
	curMonth := ""
	if curWeek >= 1 && curWeek <= len(ys.WeekTotals) {
		curWeekRange = ys.WeekTotals[curWeek-1].RangeLabel
		curMonth = ys.WeekTotals[curWeek-1].Month
	}
	s.render(w, r, "dashboard.html", map[string]any{
		"Active":         "dashboard",
		"Wide":           true,
		"Settings":       d.Settings,
		"FYYears":        fyYears(d),
		"Summary":        ys,
		"Goal":           goal,
		"Workload":       workload,
		"WorkloadPlan":   workloadPlan,
		"WorkloadLimit":  forecast.WorkdayLimitHours,
		"WeekToDate":     forecast.BuildWeekToDate(d, cal),
		"Accuracy":       forecast.BuildForecastAccuracy(d, time.Now().UTC()),
		"Projects":       projects,
		"ActiveProjects": len(activeProjects(projects)),
		"CurrentWeek":    curWeek,
		"CurrentRange":   curWeekRange,
		"CurrentMonth":   curMonth,
		"FYWeekCount":    len(ys.WeekTotals),
		"FYStart":        fyStart.Format("02.01.2006"),
		"FYEnd":          fyEnd.Format("02.01.2006"),
		"Sankey":         sankey,
		"SankeyRanges":   forecast.SankeyRanges,
		"SankeySVG":      sankeySVG(sankey),
		"FreeTimeSVG":    freeTimeSVG(sankey),
	})
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
	d := s.viewData(r)
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
			Burndown: burndownSVG(pts, ps.AvailableBudget, ps.Project.Color),
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
		if assignmentTaken(d.Projects, assignmentID, d.Settings.Year, "") {
			return errAssignmentTaken
		}
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
	startDate := validISODate(r.FormValue("startDate"))
	endDate := validISODate(r.FormValue("endDate"))
	_ = s.store.Update(func(d *models.Data) error {
		for i := range d.Projects {
			if d.Projects[i].ID == id {
				if name != "" {
					d.Projects[i].Name = name
				}
				if assignmentID != "" {
					if assignmentTaken(d.Projects, assignmentID, d.Projects[i].FiscalYear, id) {
						return errAssignmentTaken
					}
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

// handleProjectActive switches a project between active and inactive. The flag
// has its own route (and its own button) because it is not an ordinary field:
// an inactive project keeps every booked and forecast hour, but releases the
// budget that was never planned — see forecast.ProjectSummary.Released.
func (s *Server) handleProjectActive(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	active := r.FormValue("active") == "1"
	_ = s.store.Update(func(d *models.Data) error {
		for i := range d.Projects {
			if d.Projects[i].ID == id {
				d.Projects[i].Active = active
			}
		}
		return nil
	})
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
	d := s.viewData(r)
	cal := s.calendar(d)
	gs := forecast.BuildGoalSummary(d, cal)
	ys := forecast.BuildYearSummary(d, cal)
	workload := forecast.BuildWorkloadTimeline(d)

	// Cumulative booked and projected hours per month drive the progress charts
	// for the whole FY, each half-year and each quarter.
	var fyChart, h1Chart, h2Chart template.HTML
	quarterCharts := make([]template.HTML, 4)
	if len(gs.Months) == 12 {
		labels := make([]string, 12)
		proj := make([]float64, 12)
		act := make([]float64, 12)
		for i, m := range gs.Months {
			labels[i] = m.Label
			proj[i] = m.Projected
			act[i] = m.Actual
		}
		// "Today" as a fractional month index, so the curve switches from booked
		// to projected exactly where the fiscal year currently stands.
		pos := forecast.FYMonthProgress(d.Settings.Year, d.Settings.FiscalYearStartMonth)
		start, _ := forecast.FiscalYear(d.Settings.Year, d.Settings.FiscalYearStartMonth)
		clamp := func(v, max float64) float64 {
			if v < 0 {
				return 0
			}
			if v > max {
				return max
			}
			return v
		}
		fyChart = progressSVG(labels, cumulative(act), cumulative(proj), gs.TargetHours, pos, true, start)
		h1Chart = progressSVG(labels[:6], cumulative(act[:6]), cumulative(proj[:6]),
			round1(gs.TargetHours/2), clamp(pos, 6), false, start)
		h2Chart = progressSVG(labels[6:], cumulative(act[6:]), cumulative(proj[6:]),
			round1(gs.TargetHours/2), clamp(pos-6, 6), false, start.AddDate(0, 6, 0))
		for q := 0; q < 4; q++ {
			from, to := q*3, q*3+3
			quarterCharts[q] = progressSVG(labels[from:to], cumulative(act[from:to]), cumulative(proj[from:to]),
				round1(gs.TargetHours/4), clamp(pos-float64(from), 3), false, start.AddDate(0, from, 0))
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
		"Summary":         ys,
		"FYChart":         fyChart,
		"H1Chart":         h1Chart,
		"H2Chart":         h2Chart,
		"QuarterCharts":   quarterCharts,
		"FlowSVG":         goalFlowSVG(forecast.BuildGoalFlow(d, cal)),
		"Workload":        workload,
		"WorkloadSVG":     workloadTimelineSVG(workload),
		"WorkloadBack":    forecast.BuildWorkload(d, forecast.WorkloadTileMonths),
		"WorkloadLimit":   forecast.WorkdayLimitHours,
		"WorkloadDayMax":  forecast.LongDayHours,
		"ChatPresets":     chatPresets,
		"ChatPromptsJSON": template.JS(promptsJSON), // #nosec G203 -- JSON-encoded constants, no user input
		"AIConfigured":    s.aiReady(d.Settings.AI),
		"AIKeyEnv":        aiAPIKeyEnv,
	})
}

// --- Settings ---

// handleSettings always edits the fiscal year selected in the header - there is
// deliberately no second year picker on this page. It reads the real document
// even in private mode: the page holds no project data, and the form has to
// write back exactly what it shows.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	d := s.store.Snapshot()
	year := d.Settings.Year
	fy := d.FYFor(year)
	fyStart, fyEnd := forecast.FiscalYear(year, d.Settings.FiscalYearStartMonth)
	capacity := forecast.BuildFYCapacity(d, s.calendar(d), year)
	s.render(w, r, "settings.html", map[string]any{
		"Active":          "settings",
		"Settings":        d.Settings,
		"FYYears":         fyYears(d),
		"States":          holidays.States,
		"Months":          monthOptions,
		"DashboardRanges": models.DashboardRanges,
		"DataPath":        s.store.Path(),
		"DataSize":        formatBytes(s.store.FileSize()),
		"FY":              fy,
		"Capacity":        capacity,
		"FYStart":         fyStart.Format("02.01.2006"),
		"FYEnd":           fyEnd.Format("02.01.2006"),
		"AIKeyEnv":        aiAPIKeyEnv,
		"AIKeySet":        trim(os.Getenv(aiAPIKeyEnv)) != "",
		"AIKeyInStore":    trim(d.Settings.AI.APIKey) != "",
		"Foundry":         s.foundrySettings(r, d.Settings.AI.Deployment),
		"APIReadEnv":      api.ReadTokenEnv,
		"APIReadSet":      trim(os.Getenv(api.ReadTokenEnv)) != "",
		"APIWriteEnv":     api.WriteTokenEnv,
		"APIWriteSet":     trim(os.Getenv(api.WriteTokenEnv)) != "",
	})
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if trim(r.FormValue("section")) == "ai" {
		s.handleAISettingsSave(w, r)
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
	state := trim(r.FormValue("state"))
	weekly, _ := strconv.ParseFloat(normalizeNum(r.FormValue("weekly")), 64)
	fyStartMonth, fyMonthErr := strconv.Atoi(trim(r.FormValue("fyStartMonth")))
	grossHours, grossErr := strconv.ParseFloat(normalizeNum(r.FormValue("fyWeekdayHours")), 64)
	vacDays, vacErr := strconv.Atoi(trim(r.FormValue("vacationDays")))
	holDays, holErr := strconv.Atoi(trim(r.FormValue("holidayDays")))
	stdHours, stdErr := strconv.ParseFloat(normalizeNum(r.FormValue("standardTaskHours")), 64)
	dashboardRange := trim(r.FormValue("dashboardRange"))
	if r.PostForm.Has("dashboardRange") && !models.ValidDashboardRange(dashboardRange) {
		http.Error(w, "Ungültiger Dashboard-Zeitraum", http.StatusBadRequest)
		return
	}
	err := s.store.Update(func(d *models.Data) error {
		// The hour configuration always belongs to the fiscal year selected in
		// the header; this page has no year picker of its own.
		target := d.Settings.Year
		if r.PostForm.Has("dashboardRange") {
			d.Settings.DashboardRange = dashboardRange
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
		if vacErr == nil && vacDays >= 0 && vacDays <= 366 {
			fy.VacationDays = vacDays
		}
		if stdErr == nil && stdHours >= 0 {
			fy.StandardTaskHours = stdHours
		}
		// Only values that differ from the calendar are worth storing; matching it
		// again drops the override so the field keeps tracking the calendar.
		capacity := forecast.BuildFYCapacity(*d, holidays.Get(target, d.Settings.FederalState), target)
		if grossErr == nil && grossHours >= 0 {
			fy.WeekdayHours = grossHours
			if grossHours == capacity.WeekdayHoursAuto {
				fy.WeekdayHours = 0
			}
		}
		if holErr == nil && holDays >= 0 && holDays <= 366 {
			fy.HolidayDays = &holDays
			if holDays == capacity.HolidayDaysAuto {
				fy.HolidayDays = nil
			}
		}
		d.FiscalYears[target] = fy
		// Keep the vacation project's budget in sync with the vacation days.
		models.EnsureVacationProject(d, target)
		return nil
	})
	if err != nil {
		s.logger.Error("settings save failed", "error", err)
		http.Error(w, "Einstellungen konnten nicht gespeichert werden", http.StatusInternalServerError)
		return
	}
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
	// The private mode shows sample data; exporting would hand out the real
	// document behind it.
	if isPrivate(r) {
		http.Error(w, "im privaten Modus deaktiviert", http.StatusForbidden)
		return
	}
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
	// The private mode is a read-only view on sample data; switching the year
	// would write to the real document.
	if year, err := strconv.Atoi(trim(r.FormValue("year"))); err == nil && models.ValidYear(year) && !isPrivate(r) {
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

func newID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

// fyYears returns the sorted list of fiscal years offered in the header
// dropdown: every configured year plus a small range around the active one.
func fyYears(d models.Data) []int {
	set := map[int]bool{}
	for y := range d.ForecastAccuracy {
		set[y] = true
	}
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
