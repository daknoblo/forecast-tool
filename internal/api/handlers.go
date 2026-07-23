package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

// --- Read endpoints ---

// handleGetData returns the whole document. The AI API key is redacted so a
// secret that may still live in a legacy data file is never exposed.
func (s *Server) handleGetData(w http.ResponseWriter, r *http.Request) {
	d := s.store.Snapshot()
	d.Settings.AI.APIKey = ""
	s.writeJSON(w, http.StatusOK, d)
}

// handleGetSettings returns the global settings and the per-fiscal-year map.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	d := s.store.Snapshot()
	d.Settings.AI.APIKey = ""
	s.writeJSON(w, http.StatusOK, map[string]any{
		"settings":    d.Settings,
		"fiscalYears": d.FiscalYears,
	})
}

// handleListProjects lists projects. By default only the active fiscal year is
// returned; ?fiscalYear=YYYY selects another FY and ?all=true returns every FY.
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	d := s.store.Snapshot()
	q := r.URL.Query()
	projects := d.Projects
	switch {
	case strings.EqualFold(strings.TrimSpace(q.Get("all")), "true"):
		// keep every project
	case strings.TrimSpace(q.Get("fiscalYear")) != "":
		year, err := strconv.Atoi(strings.TrimSpace(q.Get("fiscalYear")))
		if err != nil || !models.ValidYear(year) {
			s.writeError(w, http.StatusBadRequest, "fiscalYear ist ungültig")
			return
		}
		projects = models.ProjectsForFY(d.Projects, year)
	default:
		projects = models.ProjectsForFY(d.Projects, d.Settings.Year)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"projects": forecast.SortedProjects(projects)})
}

// handleGetProject returns a single project by id.
func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d := s.store.Snapshot()
	for _, p := range d.Projects {
		if p.ID == id {
			s.writeJSON(w, http.StatusOK, p)
			return
		}
	}
	s.writeError(w, http.StatusNotFound, "Projekt nicht gefunden")
}

// handleListEntries returns entries filtered by the optional query parameters
// from, to (inclusive ISO dates), projectId and kind (forecast|actual).
func (s *Server) handleListEntries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err := optionalISO(q.Get("from"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "from ist kein gültiges Datum (YYYY-MM-DD)")
		return
	}
	to, err := optionalISO(q.Get("to"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "to ist kein gültiges Datum (YYYY-MM-DD)")
		return
	}
	projectID := strings.TrimSpace(q.Get("projectId"))
	kind := strings.TrimSpace(q.Get("kind"))
	if kind != "" && kind != models.KindForecast && kind != models.KindActual {
		s.writeError(w, http.StatusBadRequest, "kind muss forecast oder actual sein")
		return
	}

	d := s.store.Snapshot()
	out := make([]models.Entry, 0, len(d.Entries))
	for _, e := range d.Entries {
		if from != "" && e.Date < from {
			continue
		}
		if to != "" && e.Date > to {
			continue
		}
		if projectID != "" && e.ProjectID != projectID {
			continue
		}
		if kind != "" && entryKind(e) != kind {
			continue
		}
		out = append(out, e)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

// handleGetGoal returns the goal/capacity summary for a fiscal year (?year=,
// default the active FY).
func (s *Server) handleGetGoal(w http.ResponseWriter, r *http.Request) {
	d := s.store.Snapshot()
	if y := strings.TrimSpace(r.URL.Query().Get("year")); y != "" {
		year, err := strconv.Atoi(y)
		if err != nil || !models.ValidYear(year) {
			s.writeError(w, http.StatusBadRequest, "year ist ungültig")
			return
		}
		d.Settings.Year = year
	}
	cal := holidays.New(d.Settings.Year, d.Settings.FederalState)
	s.writeJSON(w, http.StatusOK, forecast.BuildGoalSummary(d, cal))
}

// --- Write endpoints ---

type syncEntry struct {
	Date      string  `json:"date"`
	ProjectID string  `json:"projectId"`
	Hours     float64 `json:"hours"`
	Kind      string  `json:"kind"`
}

type syncRequest struct {
	Entries []syncEntry `json:"entries"`
}

type syncSkip struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

type syncResult struct {
	Upserted int        `json:"upserted"`
	Deleted  int        `json:"deleted"`
	Skipped  []syncSkip `json:"skipped"`
}

// handleSyncEntries upserts a batch of entries. Each item is keyed by
// (date, projectId, kind); hours=0 deletes an existing entry. Items referencing
// an unknown project or a date outside the project's booking window are skipped
// and reported, so a partially-valid batch still applies its valid parts.
func (s *Server) handleSyncEntries(w http.ResponseWriter, r *http.Request) {
	var req syncRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Entries) == 0 {
		s.writeError(w, http.StatusBadRequest, "entries darf nicht leer sein")
		return
	}

	result := syncResult{Skipped: []syncSkip{}}
	err := s.store.Mutate(func(d *models.Data) error {
		projByID := make(map[string]models.Project, len(d.Projects))
		for _, p := range d.Projects {
			projByID[p.ID] = p
		}
		type entryKey struct{ date, project, kind string }
		idx := make(map[entryKey]int, len(d.Entries))
		for i, e := range d.Entries {
			idx[entryKey{e.Date, e.ProjectID, entryKind(e)}] = i
		}
		toDelete := map[int]bool{}

		for i, se := range req.Entries {
			date := strings.TrimSpace(se.Date)
			if _, perr := time.Parse("2006-01-02", date); perr != nil {
				result.Skipped = append(result.Skipped, syncSkip{i, "ungültiges Datum (YYYY-MM-DD)"})
				continue
			}
			kind := se.Kind
			if strings.TrimSpace(kind) == "" {
				kind = models.KindForecast
			}
			if kind != models.KindForecast && kind != models.KindActual {
				result.Skipped = append(result.Skipped, syncSkip{i, "kind muss forecast oder actual sein"})
				continue
			}
			if se.Hours < 0 {
				result.Skipped = append(result.Skipped, syncSkip{i, "hours darf nicht negativ sein"})
				continue
			}
			p, ok := projByID[se.ProjectID]
			if !ok {
				result.Skipped = append(result.Skipped, syncSkip{i, "unbekanntes Projekt"})
				continue
			}
			if !p.Bookable(date) {
				result.Skipped = append(result.Skipped, syncSkip{i, "Datum außerhalb des Buchungszeitraums"})
				continue
			}
			k := entryKey{date, se.ProjectID, kind}
			if se.Hours == 0 {
				if j, exists := idx[k]; exists {
					toDelete[j] = true
					delete(idx, k)
					result.Deleted++
				}
				continue
			}
			if j, exists := idx[k]; exists {
				d.Entries[j].Hours = se.Hours
				d.Entries[j].Kind = kind
				delete(toDelete, j)
			} else {
				d.Entries = append(d.Entries, models.Entry{
					Date: date, ProjectID: se.ProjectID, Hours: se.Hours, Kind: kind,
				})
				idx[k] = len(d.Entries) - 1
			}
			result.Upserted++
		}

		if len(toDelete) > 0 {
			kept := make([]models.Entry, 0, len(d.Entries))
			for i, e := range d.Entries {
				if toDelete[i] {
					continue
				}
				kept = append(kept, e)
			}
			d.Entries = kept
		}
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

// projectInput carries the mutable project fields. All fields are pointers so a
// PUT can distinguish "leave unchanged" (absent) from "set to this value"
// (present, including clearing a date with "").
type projectInput struct {
	Name        *string  `json:"name"`
	BudgetHours *float64 `json:"budgetHours"`
	Color       *string  `json:"color"`
	Active      *bool    `json:"active"`
	FiscalYear  *int     `json:"fiscalYear"`
	StartDate   *string  `json:"startDate"`
	EndDate     *string  `json:"endDate"`
}

// handleCreateProject creates a new project with a server-generated id.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var in projectInput
	if err := decodeJSON(r, &in); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := ""
	if in.Name != nil {
		name = capLen(strings.TrimSpace(*in.Name), 200)
	}
	if name == "" {
		s.writeError(w, http.StatusBadRequest, "name darf nicht leer sein")
		return
	}
	budget := 0.0
	if in.BudgetHours != nil {
		budget = *in.BudgetHours
	}
	if budget < 0 {
		s.writeError(w, http.StatusBadRequest, "budgetHours darf nicht negativ sein")
		return
	}
	color := ""
	if in.Color != nil {
		color = strings.TrimSpace(*in.Color)
	}
	if color != "" && !models.IsHexColor(color) {
		s.writeError(w, http.StatusBadRequest, "color ist keine gültige Hex-Farbe (#rgb oder #rrggbb)")
		return
	}
	startDate, endDate, derr := projectDates(in)
	if derr != nil {
		s.writeError(w, http.StatusBadRequest, derr.Error())
		return
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}

	var created models.Project
	err := s.store.Mutate(func(d *models.Data) error {
		year := d.Settings.Year
		if in.FiscalYear != nil {
			year = *in.FiscalYear
		}
		if !models.ValidYear(year) {
			return fmt.Errorf("fiscalYear %d liegt außerhalb von %d–%d", year, models.MinYear, models.MaxYear)
		}
		c := color
		if c == "" {
			used := make([]string, 0, len(d.Projects))
			for _, p := range d.Projects {
				used = append(used, p.Color)
			}
			c = models.RandomColor(used)
		}
		created = models.Project{
			ID:          newID(),
			Name:        name,
			BudgetHours: budget,
			Color:       c,
			Active:      active,
			FiscalYear:  year,
			StartDate:   startDate,
			EndDate:     endDate,
		}
		d.Projects = append(d.Projects, created)
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusCreated, created)
}

// handleUpdateProject updates the provided fields of an existing project. The
// auto-managed vacation project is locked and cannot be changed here.
func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in projectInput
	if err := decodeJSON(r, &in); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Validate the provided fields up front so bad input never reaches the store.
	var name string
	if in.Name != nil {
		name = capLen(strings.TrimSpace(*in.Name), 200)
		if name == "" {
			s.writeError(w, http.StatusBadRequest, "name darf nicht leer sein")
			return
		}
	}
	if in.BudgetHours != nil && *in.BudgetHours < 0 {
		s.writeError(w, http.StatusBadRequest, "budgetHours darf nicht negativ sein")
		return
	}
	if in.Color != nil {
		c := strings.TrimSpace(*in.Color)
		if c != "" && !models.IsHexColor(c) {
			s.writeError(w, http.StatusBadRequest, "color ist keine gültige Hex-Farbe (#rgb oder #rrggbb)")
			return
		}
	}
	if in.FiscalYear != nil && !models.ValidYear(*in.FiscalYear) {
		s.writeError(w, http.StatusBadRequest, "fiscalYear liegt außerhalb des gültigen Bereichs")
		return
	}
	startDate, endDate, derr := projectDates(in)
	if derr != nil {
		s.writeError(w, http.StatusBadRequest, derr.Error())
		return
	}

	var updated models.Project
	found, locked := false, false
	err := s.store.Mutate(func(d *models.Data) error {
		for i := range d.Projects {
			if d.Projects[i].ID != id {
				continue
			}
			found = true
			if d.Projects[i].IsVacation() {
				locked = true
				return nil
			}
			if in.Name != nil {
				d.Projects[i].Name = name
			}
			if in.BudgetHours != nil {
				d.Projects[i].BudgetHours = *in.BudgetHours
			}
			if in.Color != nil {
				d.Projects[i].Color = strings.TrimSpace(*in.Color)
			}
			if in.Active != nil {
				d.Projects[i].Active = *in.Active
			}
			if in.FiscalYear != nil {
				d.Projects[i].FiscalYear = *in.FiscalYear
			}
			if in.StartDate != nil {
				d.Projects[i].StartDate = startDate
			}
			if in.EndDate != nil {
				d.Projects[i].EndDate = endDate
			}
			updated = d.Projects[i]
		}
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !found {
		s.writeError(w, http.StatusNotFound, "Projekt nicht gefunden")
		return
	}
	if locked {
		s.writeError(w, http.StatusConflict, "Das Urlaubsprojekt ist automatisch verwaltet und kann nicht geändert werden")
		return
	}
	s.writeJSON(w, http.StatusOK, updated)
}

// handleDeleteProject deletes a project and cascades its entries. The vacation
// project cannot be deleted.
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found, locked := false, false
	err := s.store.Mutate(func(d *models.Data) error {
		out := make([]models.Project, 0, len(d.Projects))
		for _, p := range d.Projects {
			if p.ID == id {
				found = true
				if p.IsVacation() {
					locked = true
					out = append(out, p)
					continue
				}
				continue // drop the project
			}
			out = append(out, p)
		}
		if !found || locked {
			return nil
		}
		d.Projects = out
		entries := make([]models.Entry, 0, len(d.Entries))
		for _, e := range d.Entries {
			if e.ProjectID != id {
				entries = append(entries, e)
			}
		}
		d.Entries = entries
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !found {
		s.writeError(w, http.StatusNotFound, "Projekt nicht gefunden")
		return
	}
	if locked {
		s.writeError(w, http.StatusConflict, "Das Urlaubsprojekt ist automatisch verwaltet und kann nicht gelöscht werden")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "gelöscht", "id": id})
}

type utilInput struct {
	MinHours     *float64 `json:"minHours"`
	OptimalHours *float64 `json:"optimalHours"`
	OverHours    *float64 `json:"overHours"`
	MinLabel     *string  `json:"minLabel"`
	OptimalLabel *string  `json:"optimalLabel"`
	HighLabel    *string  `json:"highLabel"`
	OverLabel    *string  `json:"overLabel"`
}

type aiInput struct {
	Endpoint   *string `json:"endpoint"`
	Deployment *string `json:"deployment"`
	APIVersion *string `json:"apiVersion"`
}

type settingsInput struct {
	Year                 *int       `json:"year"`
	FederalState         *string    `json:"federalState"`
	WeeklyTargetHours    *float64   `json:"weeklyTargetHours"`
	FiscalYearStartMonth *int       `json:"fiscalYearStartMonth"`
	Utilization          *utilInput `json:"utilization"`
	AI                   *aiInput   `json:"ai"`
}

// handleUpdateSettings updates the provided global settings fields. The secret
// AI key is never accepted here; it is provided via FORECAST_AI_API_KEY.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsInput
	if err := decodeJSON(r, &in); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Year != nil && !models.ValidYear(*in.Year) {
		s.writeError(w, http.StatusBadRequest, "year liegt außerhalb des gültigen Bereichs")
		return
	}
	if in.FederalState != nil && !validState(strings.TrimSpace(*in.FederalState)) {
		s.writeError(w, http.StatusBadRequest, "federalState ist kein gültiges Bundesland-Kürzel")
		return
	}
	if in.WeeklyTargetHours != nil && *in.WeeklyTargetHours < 0 {
		s.writeError(w, http.StatusBadRequest, "weeklyTargetHours darf nicht negativ sein")
		return
	}
	if in.FiscalYearStartMonth != nil && (*in.FiscalYearStartMonth < 1 || *in.FiscalYearStartMonth > 12) {
		s.writeError(w, http.StatusBadRequest, "fiscalYearStartMonth muss zwischen 1 und 12 liegen")
		return
	}

	err := s.store.Mutate(func(d *models.Data) error {
		if in.Year != nil {
			d.Settings.Year = *in.Year
		}
		if in.FederalState != nil {
			d.Settings.FederalState = strings.TrimSpace(*in.FederalState)
		}
		if in.WeeklyTargetHours != nil {
			d.Settings.WeeklyTargetHours = *in.WeeklyTargetHours
		}
		if in.FiscalYearStartMonth != nil {
			d.Settings.FiscalYearStartMonth = *in.FiscalYearStartMonth
		}
		if u := in.Utilization; u != nil {
			applyUtilization(&d.Settings.Utilization, u)
		}
		if a := in.AI; a != nil {
			if a.Endpoint != nil {
				d.Settings.AI.Endpoint = strings.TrimSpace(*a.Endpoint)
			}
			if a.Deployment != nil {
				d.Settings.AI.Deployment = strings.TrimSpace(*a.Deployment)
			}
			if a.APIVersion != nil {
				d.Settings.AI.APIVersion = strings.TrimSpace(*a.APIVersion)
			}
			// Never keep a secret key in the data file.
			d.Settings.AI.APIKey = ""
		}
		// Keep the vacation project of the (possibly changed) active FY in sync.
		models.EnsureVacationProject(d, d.Settings.Year)
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	d := s.store.Snapshot()
	d.Settings.AI.APIKey = ""
	s.writeJSON(w, http.StatusOK, map[string]any{"settings": d.Settings})
}

// applyUtilization overlays the provided traffic-light fields onto dst,
// capping labels and rejecting nothing (validation happens in Mutate).
func applyUtilization(dst *models.UtilizationSettings, u *utilInput) {
	if u.MinHours != nil {
		dst.MinHours = *u.MinHours
	}
	if u.OptimalHours != nil {
		dst.OptimalHours = *u.OptimalHours
	}
	if u.OverHours != nil {
		dst.OverHours = *u.OverHours
	}
	if u.MinLabel != nil {
		dst.MinLabel = capLen(strings.TrimSpace(*u.MinLabel), 60)
	}
	if u.OptimalLabel != nil {
		dst.OptimalLabel = capLen(strings.TrimSpace(*u.OptimalLabel), 60)
	}
	if u.HighLabel != nil {
		dst.HighLabel = capLen(strings.TrimSpace(*u.HighLabel), 60)
	}
	if u.OverLabel != nil {
		dst.OverLabel = capLen(strings.TrimSpace(*u.OverLabel), 60)
	}
}

type fySettingsInput struct {
	TargetHours       *float64 `json:"targetHours"`
	VacationDaysH1    *int     `json:"vacationDaysH1"`
	VacationDaysH2    *int     `json:"vacationDaysH2"`
	StandardTaskLabel *string  `json:"standardTaskLabel"`
	StandardTaskHours *float64 `json:"standardTaskHours"`
}

// handleUpdateFYSettings upserts the settings of one fiscal year and keeps that
// year's vacation project budget in sync.
func (s *Server) handleUpdateFYSettings(w http.ResponseWriter, r *http.Request) {
	year, err := strconv.Atoi(r.PathValue("year"))
	if err != nil || !models.ValidYear(year) {
		s.writeError(w, http.StatusBadRequest, "year ist ungültig")
		return
	}
	var in fySettingsInput
	if err := decodeJSON(r, &in); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.TargetHours != nil && *in.TargetHours < 0 {
		s.writeError(w, http.StatusBadRequest, "targetHours darf nicht negativ sein")
		return
	}
	if in.VacationDaysH1 != nil && (*in.VacationDaysH1 < 0 || *in.VacationDaysH1 > 366) {
		s.writeError(w, http.StatusBadRequest, "vacationDaysH1 muss zwischen 0 und 366 liegen")
		return
	}
	if in.VacationDaysH2 != nil && (*in.VacationDaysH2 < 0 || *in.VacationDaysH2 > 366) {
		s.writeError(w, http.StatusBadRequest, "vacationDaysH2 muss zwischen 0 und 366 liegen")
		return
	}
	if in.StandardTaskHours != nil && *in.StandardTaskHours < 0 {
		s.writeError(w, http.StatusBadRequest, "standardTaskHours darf nicht negativ sein")
		return
	}

	var saved models.FiscalYearSettings
	err = s.store.Mutate(func(d *models.Data) error {
		if d.FiscalYears == nil {
			d.FiscalYears = map[int]models.FiscalYearSettings{}
		}
		fy := d.FYFor(year)
		if in.TargetHours != nil {
			fy.TargetHours = *in.TargetHours
		}
		if in.VacationDaysH1 != nil {
			fy.VacationDaysH1 = *in.VacationDaysH1
		}
		if in.VacationDaysH2 != nil {
			fy.VacationDaysH2 = *in.VacationDaysH2
		}
		if in.StandardTaskLabel != nil {
			fy.StandardTaskLabel = capLen(strings.TrimSpace(*in.StandardTaskLabel), 200)
		}
		if in.StandardTaskHours != nil {
			fy.StandardTaskHours = *in.StandardTaskHours
		}
		d.FiscalYears[year] = fy
		models.EnsureVacationProject(d, year)
		saved = fy
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"fiscalYear":          year,
		"settings":            saved,
		"vacationBudgetHours": saved.VacationBudgetHours(),
	})
}

// projectDates validates the optional start/end date inputs and returns the
// (possibly empty) values to store.
func projectDates(in projectInput) (start, end string, err error) {
	if in.StartDate != nil {
		start, err = optionalISO(*in.StartDate)
		if err != nil {
			return "", "", fmt.Errorf("startDate ist kein gültiges Datum (YYYY-MM-DD)")
		}
	}
	if in.EndDate != nil {
		end, err = optionalISO(*in.EndDate)
		if err != nil {
			return "", "", fmt.Errorf("endDate ist kein gültiges Datum (YYYY-MM-DD)")
		}
	}
	return start, end, nil
}

// entryKind returns the effective kind of an entry, defaulting a blank kind to
// forecast for backwards compatibility.
func entryKind(e models.Entry) string {
	if e.Kind == "" {
		return models.KindForecast
	}
	return e.Kind
}
