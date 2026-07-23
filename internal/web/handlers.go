package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/daknoblo/forecast-tool/internal/ai"
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
	tpl    *template.Template
	logger *slog.Logger

	mu      sync.Mutex
	calKey  string
	calData *holidays.Calendar
}

// NewServer parses templates and returns a ready-to-mount handler. The logger is
// used for operational logging (e.g. AI endpoint usage); if nil, slog.Default()
// is used.
func NewServer(store *storage.Store, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	funcs := template.FuncMap{
		"hours":   formatHours,
		"appName": func() string { return AppName },
		"pct":     func(f float64) string { return formatHours(f) + " %" },
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
		"add": func(a, b int) int { return a + b },
		"barWidth": func(pct float64) string {
			if pct > 100 {
				pct = 100
			}
			if pct < 0 {
				pct = 0
			}
			return formatHours(pct)
		},
	}
	tpl, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{store: store, tpl: tpl, logger: logger}, nil
}

// Handler builds the HTTP routing mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))

	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /week", s.handleWeekRedirect)
	mux.HandleFunc("GET /week/{week}", s.handleWeek)
	mux.HandleFunc("POST /week/{week}", s.handleWeekSave)
	mux.HandleFunc("GET /projects", s.handleProjects)
	mux.HandleFunc("POST /projects", s.handleProjectCreate)
	mux.HandleFunc("POST /projects/{id}/update", s.handleProjectUpdate)
	mux.HandleFunc("POST /projects/{id}/delete", s.handleProjectDelete)
	mux.HandleFunc("GET /goal", s.handleGoal)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings", s.handleSettingsSave)
	mux.HandleFunc("GET /export", s.handleExport)
	mux.HandleFunc("GET /data", s.handleData)
	mux.HandleFunc("POST /data", s.handleDataSave)
	mux.HandleFunc("POST /data/ai", s.handleDataAI)
	mux.HandleFunc("POST /data/reset", s.handleDataReset)
	mux.HandleFunc("POST /fy", s.handleSetActiveFY)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// JSON API for external clients (token-protected; the HTML UI stays open).
	mux.Handle("/api/", api.New(s.store, s.logger))

	return mux
}

func (s *Server) calendar(d models.Data) *holidays.Calendar {
	key := strconv.Itoa(d.Settings.Year) + "|" + d.Settings.FederalState
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calData == nil || s.calKey != key {
		s.calData = holidays.New(d.Settings.Year, d.Settings.FederalState)
		s.calKey = key
	}
	return s.calData
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// --- Dashboard ---

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	d := s.store.Snapshot()
	d.Projects = models.ProjectsForFY(d.Projects, d.Settings.Year)
	ys := forecast.BuildYearSummary(d, s.calendar(d))
	projects := forecast.SortedProjects(d.Projects)
	fyStart, fyEnd := forecast.FiscalYear(d.Settings.Year, d.Settings.FiscalYearStartMonth)
	sankey := forecast.BuildSankey(d, r.URL.Query().Get("sankey"))
	s.render(w, "dashboard.html", map[string]any{
		"Active":       "dashboard",
		"Wide":         true,
		"Settings":     d.Settings,
		"FYYears":      fyYears(d),
		"Summary":      ys,
		"Projects":     projects,
		"CurrentWeek":  forecast.CurrentFYWeek(d.Settings.Year, d.Settings.FiscalYearStartMonth),
		"FYStart":      fyStart.Format("02.01.2006"),
		"FYEnd":        fyEnd.Format("02.01.2006"),
		"Sankey":       sankey,
		"SankeyRanges": forecast.SankeyRanges,
		"SankeySVG":    sankeySVG(sankey),
	})
}

// --- Week views ---

func (s *Server) handleWeekRedirect(w http.ResponseWriter, r *http.Request) {
	d := s.store.Snapshot()
	http.Redirect(w, r, "/week/"+strconv.Itoa(forecast.CurrentFYWeek(d.Settings.Year, d.Settings.FiscalYearStartMonth)), http.StatusFound)
}

func (s *Server) handleWeek(w http.ResponseWriter, r *http.Request) {
	d := s.store.Snapshot()
	d.Projects = models.ProjectsForFY(d.Projects, d.Settings.Year)
	start := clampWeek(r.PathValue("week"), d.Settings)
	weeks := spanWeeks(r)
	cal := s.calendar(d)
	sv := forecast.BuildSpan(d, cal, start, weeks)
	projects := forecast.SortedProjects(activeProjects(d.Projects))
	ys := forecast.BuildYearSummary(d, cal)
	spanStart, spanEnd := "", ""
	if len(sv.Days) > 0 {
		spanStart = sv.Days[0].Date
		spanEnd = sv.Days[len(sv.Days)-1].Date
	}
	burn := forecast.BuildSpanBurn(ys.Projects, spanStart, spanEnd)
	budgetLeft := map[string]float64{}
	for _, p := range ys.Projects {
		budgetLeft[p.Project.ID] = round1(p.Project.BudgetHours - p.Consumed)
	}
	s.render(w, "week.html", map[string]any{
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
		// Project lookup for booking-window enforcement.
		projByID := make(map[string]models.Project, len(data.Projects))
		for _, p := range data.Projects {
			projByID[p.ID] = p
		}
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
			if p, ok := projByID[k.project]; ok && !p.Bookable(k.date) {
				continue // outside the project's booking window
			}
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

// --- Projects ---

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	d := s.store.Snapshot()
	d.Projects = models.ProjectsForFY(d.Projects, d.Settings.Year)
	ys := forecast.BuildYearSummary(d, s.calendar(d))

	type projView struct {
		Summary  forecast.ProjectSummary
		Burndown template.HTML
	}
	var views []projView
	for _, ps := range ys.Projects {
		pts := forecast.BuildBurndown(d, ps.Project.ID, ps.StartDate, ps.EndDate, ps.Project.BudgetHours)
		views = append(views, projView{
			Summary:  ps,
			Burndown: burndownSVG(pts, ps.Project.BudgetHours, ps.Project.Color),
		})
	}
	sort.Slice(views, func(i, j int) bool {
		return views[i].Summary.Project.Name < views[j].Summary.Project.Name
	})

	s.render(w, "projects.html", map[string]any{
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
	taskID := capLen(trim(r.FormValue("taskId")), 100)
	if name == "" || taskID == "" {
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
			ID:          newID(),
			TaskID:      taskID,
			Name:        name,
			BudgetHours: budget,
			Color:       models.RandomColor(used),
			Active:      true,
			FiscalYear:  d.Settings.Year,
			StartDate:   startDate,
			EndDate:     endDate,
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
	taskID := capLen(trim(r.FormValue("taskId")), 100)
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
				// The vacation project is auto-managed: its budget comes from the
				// FY settings and it must not be renamed or reconfigured here.
				if d.Projects[i].IsVacation() {
					return nil
				}
				if name != "" {
					d.Projects[i].Name = name
				}
				if taskID != "" {
					d.Projects[i].TaskID = taskID
				}
				d.Projects[i].BudgetHours = budget
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
	d := s.store.Snapshot()
	cal := s.calendar(d)
	gs := forecast.BuildGoalSummary(d, cal)
	d.Projects = models.ProjectsForFY(d.Projects, d.Settings.Year)
	ys := forecast.BuildYearSummary(d, cal)

	// Cumulative projected hours per month drive the progress charts for the
	// whole FY and each half-year.
	var fyChart, h1Chart, h2Chart template.HTML
	if len(gs.Months) == 12 {
		labels := make([]string, 12)
		proj := make([]float64, 12)
		for i, m := range gs.Months {
			labels[i] = m.Label
			proj[i] = m.Projected
		}
		fyChart = progressSVG(labels, cumulative(proj), gs.TargetHours)
		h1Chart = progressSVG(labels[:6], cumulative(proj[:6]), round1(gs.TargetHours/2))
		h2Chart = progressSVG(labels[6:], cumulative(proj[6:]), round1(gs.TargetHours/2))
	}

	s.render(w, "goal.html", map[string]any{
		"Active":     "goal",
		"Settings":   d.Settings,
		"FYYears":    fyYears(d),
		"Goal":       gs,
		"WeekTotals": ys.WeekTotals,
		"FYChart":    fyChart,
		"H1Chart":    h1Chart,
		"H2Chart":    h2Chart,
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
	s.render(w, "settings.html", map[string]any{
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
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
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
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
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
		if models.ValidYear(year) {
			d.Settings.Year = year
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
		fy := d.FYFor(d.Settings.Year)
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
		d.FiscalYears[d.Settings.Year] = fy
		// Keep the vacation project's budget in sync with the vacation days.
		models.EnsureVacationProject(d, d.Settings.Year)
		return nil
	})
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

// handleData shows the raw JSON editor so the whole document can be edited in
// the browser (e.g. with JSON generated by an AI assistant).
func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	b, err := s.store.Marshal()
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	s.renderData(w, string(b), "", "", "")
}

// handleDataReset removes all projects and bookings (entries) while keeping
// every setting intact (global settings and per-fiscal-year goals). The
// destructive action is guarded by a confirmation dialog in the browser before
// the form is submitted.
func (s *Server) handleDataReset(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Reset(time.Now().Year()); err != nil {
		http.Error(w, "reset failed", http.StatusInternalServerError)
		return
	}
	b, err := s.store.Marshal()
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	s.renderData(w, string(b), "", "", "Alle Daten wurden zurückgesetzt. Es wurde eine leere JSON geschrieben.")
}

// handleDataSave validates the submitted JSON and replaces the whole document
// when it is valid; otherwise it re-renders the editor with the error and keeps
// the user's input so nothing is lost.
func (s *Server) handleDataSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	raw := r.FormValue("json")
	if err := s.store.ReplaceJSON([]byte(raw)); err != nil {
		s.renderData(w, raw, "", err.Error(), "")
		return
	}
	// Re-marshal to show the normalized, canonical form after a successful save.
	b, err := s.store.Marshal()
	if err != nil {
		b = []byte(raw)
	}
	s.renderData(w, string(b), "", "", "Daten gespeichert und validiert.")
}

// handleDataAI sends the prompt together with the current JSON to the configured
// AI endpoint and places the returned JSON into the editor. Nothing is saved
// automatically; the result is validated so the user knows whether it can be
// stored, but they must press "Speichern" to persist it.
func (s *Server) handleDataAI(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	prompt := trim(r.FormValue("prompt"))
	currentJSON := r.FormValue("json")
	if currentJSON == "" {
		if b, err := s.store.Marshal(); err == nil {
			currentJSON = string(b)
		}
	}
	if prompt == "" {
		s.renderData(w, currentJSON, prompt, "Bitte gib einen Prompt ein.", "")
		return
	}

	cfg := effectiveAI(s.store.Snapshot().Settings.AI)
	s.logger.Info("ai update requested",
		"remoteAddr", r.RemoteAddr, "promptChars", len(prompt), "inputJSONChars", len(currentJSON))
	result, err := ai.Generate(r.Context(), cfg, prompt, currentJSON, s.logger)
	if err != nil {
		s.logger.Error("ai update failed", "error", err)
		s.renderData(w, currentJSON, prompt, err.Error(), "")
		return
	}

	// Expand any compact forecastPlan directives into concrete weekday entries
	// before validating, so the model never has to enumerate hundreds of days.
	startMonth := s.store.Snapshot().Settings.FiscalYearStartMonth
	expanded, exErr := ai.ExpandPlan([]byte(result), startMonth)
	if exErr != nil {
		s.logger.Warn("ai forecastPlan expansion failed", "error", exErr)
		s.renderData(w, result, prompt, "KI-Antwort konnte nicht verarbeitet werden: "+exErr.Error(), "")
		return
	}
	result = string(expanded)

	// Validate the AI output but keep it in the editor regardless, so the user
	// can review and fix it before saving.
	if vErr := s.store.ValidateJSON([]byte(result)); vErr != nil {
		s.logger.Warn("ai update returned invalid json", "error", vErr, "resultChars", len(result))
		s.renderData(w, result, prompt, "KI-Antwort ist noch nicht gültig – bitte prüfen und korrigieren: "+vErr.Error(), "")
		return
	}
	s.logger.Info("ai update succeeded", "resultChars", len(result))
	s.renderData(w, result, "", "", "KI-Antwort eingefügt und validiert. Prüfe das Ergebnis und klicke auf „Speichern“, um es zu übernehmen.")
}

// renderData renders the JSON editor page with optional error/success messages.
func (s *Server) renderData(w http.ResponseWriter, jsonText, prompt, errMsg, okMsg string) {
	d := s.store.Snapshot()
	s.render(w, "data.html", map[string]any{
		"Active":       "data",
		"Settings":     d.Settings,
		"FYYears":      fyYears(d),
		"JSON":         jsonText,
		"Prompt":       prompt,
		"AIConfigured": aiConfigured(effectiveAI(d.Settings.AI)),
		"Error":        errMsg,
		"Success":      okMsg,
	})
}

// aiConfigured reports whether the minimum AI endpoint settings are present.
func aiConfigured(a models.AISettings) bool {
	return trim(a.Endpoint) != "" && trim(a.Deployment) != "" && trim(a.APIKey) != ""
}

// aiAPIKeyEnv is the NAME of the environment variable that supplies the secret
// AI API key. The key itself is never stored in code or the data file.
const aiAPIKeyEnv = "FORECAST_AI_API_KEY" // #nosec G101 -- env var name, not a credential

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
