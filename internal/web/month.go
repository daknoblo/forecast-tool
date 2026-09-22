package web

import (
	"net/http"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
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
	s.render(w, r, "month.html", map[string]any{
		"Active": "month", "Wide": true, "Settings": d.Settings, "FYYears": fyYears(d),
		"Plan": plan,
	})
}
