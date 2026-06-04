package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/storage"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Server wires storage, templates and HTTP routing together.
type Server struct {
	store *storage.Store
	tpl   *template.Template

	mu      sync.Mutex
	calKey  string
	calData *holidays.Calendar
}

// NewServer parses templates and returns a ready-to-mount handler.
func NewServer(store *storage.Store) (*Server, error) {
	funcs := template.FuncMap{
		"hours": formatHours,
		"pct":   func(f float64) string { return formatHours(f) + " %" },
		"cellName": func(projectID, date string) string {
			return "h_" + projectID + "_" + date
		},
		"cellNameActual": func(projectID, date string) string {
			return "a_" + projectID + "_" + date
		},
		"cellHours": func(cell forecast.DayCell, projectID string) float64 {
			return cell.Hours[projectID]
		},
		"cellActual": func(cell forecast.DayCell, projectID string) float64 {
			return cell.Actual[projectID]
		},
		"weekTotal": func(totals map[string]float64, projectID string) float64 {
			return totals[projectID]
		},
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
	return &Server{store: store, tpl: tpl}, nil
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
	mux.HandleFunc("POST /fy", s.handleSetActiveFY)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

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
	ys := forecast.BuildYearSummary(d)
	projects := forecast.SortedProjects(d.Projects)
	fyStart, fyEnd := forecast.FiscalYear(d.Settings.Year, d.Settings.FiscalYearStartMonth)
	s.render(w, "dashboard.html", map[string]any{
		"Active":      "dashboard",
		"Settings":    d.Settings,
		"FYYears":     fyYears(d),
		"Summary":     ys,
		"Projects":    projects,
		"CurrentWeek": forecast.CurrentFYWeek(d.Settings.Year, d.Settings.FiscalYearStartMonth),
		"FYStart":     fyStart.Format("02.01.2006"),
		"FYEnd":       fyEnd.Format("02.01.2006"),
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
	week := clampWeek(r.PathValue("week"), d.Settings)
	cal := s.calendar(d)
	wv := forecast.BuildWeek(d, cal, week)
	projects := forecast.SortedProjects(activeProjects(d.Projects))
	s.render(w, "week.html", map[string]any{
		"Active":      "week",
		"Settings":    d.Settings,
		"FYYears":     fyYears(d),
		"Week":        wv,
		"MaxWeek":     forecast.FYWeeks(d.Settings.Year, d.Settings.FiscalYearStartMonth),
		"Projects":    projects,
		"AllProjects": forecast.SortedProjects(d.Projects),
	})
}

func (s *Server) handleWeekSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	d := s.store.Snapshot()
	week := clampWeek(r.PathValue("week"), d.Settings)
	monday := forecast.FYWeekMonday(d.Settings.Year, d.Settings.FiscalYearStartMonth, week)

	// Collect the set of dates belonging to this week (Mon-Fri).
	weekDates := map[string]bool{}
	for i := 0; i < 5; i++ {
		weekDates[monday.AddDate(0, 0, i).Format("2006-01-02")] = true
	}

	type key struct{ date, project string }
	newHours := map[key]float64{}
	newActual := map[key]float64{}
	for name, vals := range r.Form {
		if len(name) < 3 || (name[:2] != "h_" && name[:2] != "a_") {
			continue
		}
		// h_{projectID}_{YYYY-MM-DD} (forecast) or a_{...} (actual)
		isActual := name[:2] == "a_"
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
		if isActual {
			newActual[key{date, projectID}] = h
		} else {
			newHours[key{date, projectID}] = h
		}
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
					Date: k.date, ProjectID: k.project, Hours: newHours[k], Kind: models.KindForecast,
				})
			}
		}
		akeys := make([]key, 0, len(newActual))
		for k := range newActual {
			akeys = append(akeys, k)
		}
		sort.Slice(akeys, func(i, j int) bool {
			if akeys[i].date != akeys[j].date {
				return akeys[i].date < akeys[j].date
			}
			return akeys[i].project < akeys[j].project
		})
		for _, k := range akeys {
			if newActual[k] > 0 {
				data.Entries = append(data.Entries, models.Entry{
					Date: k.date, ProjectID: k.project, Hours: newActual[k], Kind: models.KindActual,
				})
			}
		}
		return nil
	})
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/week/"+strconv.Itoa(week), http.StatusSeeOther)
}

// --- Projects ---

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	d := s.store.Snapshot()
	d.Projects = models.ProjectsForFY(d.Projects, d.Settings.Year)
	ys := forecast.BuildYearSummary(d)

	type projView struct {
		Summary  forecast.ProjectSummary
		Burndown template.HTML
	}
	var views []projView
	for _, ps := range ys.Projects {
		pts := forecast.BuildBurndown(d, ps.Project.ID, ps.Project.BudgetHours)
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
	name := trim(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/projects", http.StatusSeeOther)
		return
	}
	budget, _ := strconv.ParseFloat(normalizeNum(r.FormValue("budget")), 64)
	_ = s.store.Update(func(d *models.Data) error {
		used := make([]string, 0, len(d.Projects))
		for _, p := range d.Projects {
			used = append(used, p.Color)
		}
		d.Projects = append(d.Projects, models.Project{
			ID:          newID(),
			Name:        name,
			BudgetHours: budget,
			Color:       models.RandomColor(used),
			Active:      true,
			FiscalYear:  d.Settings.Year,
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
	name := trim(r.FormValue("name"))
	budget, _ := strconv.ParseFloat(normalizeNum(r.FormValue("budget")), 64)
	color := trim(r.FormValue("color"))
	active := r.FormValue("active") != ""
	_ = s.store.Update(func(d *models.Data) error {
		for i := range d.Projects {
			if d.Projects[i].ID == id {
				if name != "" {
					d.Projects[i].Name = name
				}
				d.Projects[i].BudgetHours = budget
				if color != "" {
					d.Projects[i].Color = color
				}
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
		for _, p := range d.Projects {
			if p.ID != id {
				out = append(out, p)
			}
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
	s.render(w, "goal.html", map[string]any{
		"Active":   "goal",
		"Settings": d.Settings,
		"FYYears":  fyYears(d),
		"Goal":     gs,
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
		"Active":   "settings",
		"Settings": d.Settings,
		"FYYears":  fyYears(d),
		"States":   holidays.States,
		"Months":   monthOptions,
		"DataPath": s.store.Path(),
		"DataSize": formatBytes(s.store.FileSize()),
		"ViewYear": viewYear,
		"PrevYear": viewYear - 1,
		"NextYear": viewYear + 1,
		"IsActive": viewYear == d.Settings.Year,
		"FY":       fy,
		"FYStart":  fyStart.Format("02.01.2006"),
		"FYEnd":    fyEnd.Format("02.01.2006"),
		"H1Label":  halfLabel(fyStart, h1End),
		"H2Label":  halfLabel(h2Start, fyEnd),
	})
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	year, _ := strconv.Atoi(trim(r.FormValue("year")))
	state := trim(r.FormValue("state"))
	weekly, _ := strconv.ParseFloat(normalizeNum(r.FormValue("weekly")), 64)
	fyStartMonth, fyMonthErr := strconv.Atoi(trim(r.FormValue("fyStartMonth")))
	fyTarget, fyErr := strconv.ParseFloat(normalizeNum(r.FormValue("fyTarget")), 64)
	vacH1, vacH1Err := strconv.Atoi(trim(r.FormValue("vacationH1")))
	vacH2, vacH2Err := strconv.Atoi(trim(r.FormValue("vacationH2")))
	stdLabel := trim(r.FormValue("standardTaskLabel"))
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
		fy.StandardTaskLabel = stdLabel
		if stdErr == nil && stdHours >= 0 {
			fy.StandardTaskHours = stdHours
		}
		d.FiscalYears[d.Settings.Year] = fy
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
			return nil
		})
	}
	dest := trim(r.Header.Get("Referer"))
	if dest == "" {
		dest = "/"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// --- helpers ---

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
