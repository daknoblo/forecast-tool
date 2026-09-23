package web

import (
	"net/http"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/models"
)

func (s *Server) handleMonth(w http.ResponseWriter, r *http.Request) {
	d := s.viewData(r)
	now := time.Now().UTC()
	month := now
	if value := r.URL.Query().Get("month"); value != "" {
		var err error
		month, err = time.Parse("2006-01", value)
		if err != nil {
			http.Error(w, "Ungültiger Monat (JJJJ-MM erwartet).", http.StatusBadRequest)
			return
		}
	}
	plan := forecast.BuildMonthPlan(d, s.calendar(d), month, now)
	month, _ = time.Parse("2006-01", plan.Month)
	prompt := monthPrompt(d)
	systemPrompt := monthSystemPrompt(d)
	saved := d.SavedMonthPlans[plan.Month] != "" && !isPrivate(r)
	if saved {
		plan = forecast.BuildStoredMonthPlan(d, s.calendar(d), month, now)
	}
	if isPrivate(r) {
		prompt = forecast.DefaultMonthPlanningPrompt
		systemPrompt = forecast.MonthPlanningSystemPrompt
	}
	page := map[string]any{
		"Active": "month", "Wide": true, "Settings": d.Settings, "FYYears": fyYears(d),
		"Plan": plan, "Prompt": prompt, "PromptLimit": models.MaxMonthPlanningPrompt,
		"SystemPrompt": systemPrompt, "Saved": saved,
		"ExplanationFormat": forecast.MonthPlanningExplanationFormat,
		"CanGenerate":       !isPrivate(r) && !month.AddDate(0, 1, 0).Before(now) && s.aiReady(d.Settings.AI),
		"AIReady":           s.aiReady(d.Settings.AI),
	}
	if token := r.URL.Query().Get("preview"); token != "" && !isPrivate(r) {
		preview, err := s.monthPreview(token, d, month, now)
		if err != nil {
			page["PlanningError"] = err.Error()
		} else {
			page["Plan"] = s.previewMonthPlan(d, month, now, preview)
			page["Preview"] = token
			page["Prompt"] = preview.Prompt
			page["SystemPrompt"] = preview.SystemPrompt
			page["CanSave"] = len(preview.Plan.Unallocated) == 0
			page["Explanation"] = preview.Plan.Explanation
			page["Deployment"] = preview.Deployment
		}
	}
	s.render(w, r, "month.html", page)
}

func (s *Server) previewMonthPlan(d models.Data, month, now time.Time, preview monthAIPreview) forecast.MonthPlan {
	original := forecast.BuildStoredMonthPlan(d, s.calendar(d), month, now)
	vacation := make(map[string]bool)
	for _, project := range d.Projects {
		vacation[project.ID] = project.IsVacation()
	}
	entries := make([]models.Entry, 0, len(d.Entries)+len(preview.Plan.Entries))
	end := month.AddDate(0, 1, 0).Format("2006-01-02")
	from := month.Format("2006-01-02")
	for _, entry := range d.Entries {
		if isVacation, known := vacation[entry.ProjectID]; !known || isVacation || entry.Date < preview.Context.Today || entry.Date < from || entry.Date >= end {
			entries = append(entries, entry)
		}
	}
	d.Entries = append(entries, preview.Plan.Entries...)
	plan := forecast.BuildStoredMonthPlan(d, s.calendar(d), month, now)
	names := make(map[string]string)
	for _, project := range d.Projects {
		names[project.ID] = project.Name
	}
	for i := range plan.Weeks {
		week := &plan.Weeks[i]
		week.Stored, week.Work, week.Free, week.Over = original.Weeks[i].Stored, original.Weeks[i].Work, original.Weeks[i].Free, original.Weeks[i].Over
		week.Projects = original.Weeks[i].Projects
		for j := range week.Days {
			day := &week.Days[j]
			day.Stored = original.Weeks[i].Days[j].Stored
			if day.InMonth && !day.Past {
				day.Estimated = true
				for k := range day.Events {
					if !day.Events[k].Vacation {
						day.Events[k].Estimated = true
						day.Events[k].Basis = "KI-Vorschau"
					}
				}
			}
		}
		for _, pending := range preview.Plan.Unallocated {
			if pending.WeekStart == week.Days[0].Date {
				week.Unallocated += pending.Hours
				week.Pending = append(week.Pending, forecast.MonthEvent{
					ProjectID: pending.ProjectID, Name: names[pending.ProjectID], Hours: pending.Hours, Basis: pending.Reason,
				})
			}
		}
	}
	return plan
}
