package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/models"
)

func (s *Server) handleGetForecastAccuracy(w http.ResponseWriter, r *http.Request) {
	year, err := strconv.Atoi(r.PathValue("year"))
	if err != nil || !models.ValidYear(year) {
		s.writeError(w, http.StatusBadRequest, "year ist ungültig")
		return
	}
	d := s.store.Snapshot()
	d.Settings.Year = year
	s.writeJSON(w, http.StatusOK, forecast.BuildForecastAccuracy(d, time.Now().UTC()))
}

func (s *Server) handlePutForecastAccuracy(w http.ResponseWriter, r *http.Request) {
	year, err := strconv.Atoi(r.PathValue("year"))
	if err != nil || !models.ValidYear(year) {
		s.writeError(w, http.StatusBadRequest, "year ist ungültig")
		return
	}
	var in struct {
		Percentage *float64 `json:"percentage"`
		AsOf       string   `json:"asOf"`
	}
	if err := decodeJSON(r, &in); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Percentage == nil {
		s.writeError(w, http.StatusBadRequest, "percentage ist erforderlich (0 bis 100)")
		return
	}
	now := time.Now().UTC()
	status := http.StatusInternalServerError
	var summary forecast.AccuracySummary
	err = s.store.Mutate(func(d *models.Data) error {
		value := models.ForecastAccuracy{
			Percentage: *in.Percentage, AsOf: in.AsOf,
			FiscalYearStartMonth: d.Settings.FiscalYearStartMonth,
		}
		if err := forecast.ValidateForecastAccuracy(year, value, now); err != nil {
			status = http.StatusBadRequest
			return err
		}
		if previous, ok := d.ForecastAccuracy[year]; ok && previous.AsOf > value.AsOf {
			status = http.StatusConflict
			return fmt.Errorf("Ein neuerer Forecast-Accuracy-Wert ist bereits gespeichert")
		}
		if d.ForecastAccuracy == nil {
			d.ForecastAccuracy = make(map[int]models.ForecastAccuracy)
		}
		d.ForecastAccuracy[year] = value
		view := *d
		view.Settings.Year = year
		summary = forecast.BuildForecastAccuracy(view, now)
		return nil
	})
	if err != nil {
		if status == http.StatusInternalServerError {
			s.logger.Error("forecast accuracy save failed", "error", err)
			s.writeError(w, status, "Forecast Accuracy konnte nicht gespeichert werden")
		} else {
			s.writeError(w, status, err.Error())
		}
		return
	}
	s.writeJSON(w, http.StatusOK, summary)
}
