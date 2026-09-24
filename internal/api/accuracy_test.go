package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/storage"
)

func TestForecastAccuracyAPI(t *testing.T) {
	st := newTestStore(t)
	h := newTestServer(t, st, readTok, writeTok)
	year := setFYAroundToday(t, st)
	path := fmt.Sprintf("/api/v1/forecast-accuracy/%d", year)
	date := time.Now().UTC().Format("2006-01-02")
	valid := map[string]any{"percentage": 100, "asOf": date}
	for _, tc := range []struct {
		method, token string
		status        int
	}{
		{http.MethodGet, "", 401}, {http.MethodPut, "", 401},
		{http.MethodGet, readTok, 200}, {http.MethodPut, readTok, 403},
		{http.MethodPut, writeTok, 200}, {http.MethodGet, writeTok, 200},
	} {
		if rr := do(t, h, tc.method, path, tc.token, valid); rr.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.method, rr.Code, rr.Body.String())
		}
	}
	for _, body := range []any{
		nil, map[string]any{}, map[string]any{"percentage": nil, "asOf": date},
		map[string]any{"percentage": "100", "asOf": date},
		map[string]any{"percentage": 101, "asOf": date},
		map[string]any{"percentage": -1, "asOf": date},
		map[string]any{"percentage": 100}, map[string]any{"percentage": 100, "asOf": nil},
		map[string]any{"percentage": 100, "asOf": "invalid"},
		map[string]any{"percentage": 100, "asOf": "2026-02-30"},
		map[string]any{"percentage": 100, "asOf": "1999-01-01"},
		map[string]any{"percentage": 100, "asOf": time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")},
		map[string]any{"percentage": 100, "asOf": date, "unknown": true},
	} {
		before := st.Snapshot()
		rr := do(t, h, http.MethodPut, path, writeTok, body)
		if rr.Code != 400 || !reflect.DeepEqual(before, st.Snapshot()) {
			t.Fatalf("invalid body %v: %d %s, or changed storage", body, rr.Code, rr.Body.String())
		}
	}
	for _, invalidYear := range []string{"nope", "1999", "2101"} {
		for _, method := range []string{http.MethodPut, http.MethodGet} {
			if rr := do(t, h, method, "/api/v1/forecast-accuracy/"+invalidYear, writeTok, valid); rr.Code != 400 {
				t.Fatalf("invalid year: %d", rr.Code)
			}
		}
	}
	old := map[string]any{"percentage": 99, "asOf": time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")}
	if rr := do(t, h, http.MethodPut, path, writeTok, old); rr.Code != 409 {
		t.Fatalf("old snapshot: %d %s", rr.Code, rr.Body.String())
	}
	for _, percent := range []float64{91.67, 0, 100} {
		body := map[string]any{"percentage": percent, "asOf": date}
		rr := do(t, h, http.MethodPut, path, writeTok, body)
		if rr.Code != 200 {
			t.Fatal(rr.Body.String())
		}
		var got forecast.AccuracySummary
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if !got.HasData || !got.HasProjection || got.Percentage != percent || got.AsOf != date {
			t.Fatalf("unexpected response: %+v", got)
		}
	}
	for _, y := range []int{year, year - 1} {
		var got forecast.AccuracySummary
		rr := do(t, h, http.MethodGet, fmt.Sprintf("/api/v1/forecast-accuracy/%d", y), readTok, nil)
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.HasData != (y == year) {
			t.Fatalf("FY isolation: %+v", got)
		}
	}
	reloaded, err := storage.New(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.Snapshot().ForecastAccuracy, reloaded.Snapshot().ForecastAccuracy) {
		t.Fatal("accuracy not persisted")
	}
	var exported models.Data
	rr := do(t, h, http.MethodGet, "/api/v1/data", readTok, nil)
	if err := json.Unmarshal(rr.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exported.ForecastAccuracy, st.Snapshot().ForecastAccuracy) {
		t.Fatal("missing from export")
	}
	snapshot := st.Snapshot()
	delete(snapshot.ForecastAccuracy, year)
	if len(st.Snapshot().ForecastAccuracy) != 1 {
		t.Fatal("snapshot aliases stored map")
	}
	before := st.Snapshot()
	if err := os.Mkdir(st.Path()+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	rr = do(t, h, http.MethodPut, path, writeTok, map[string]any{"percentage": 50, "asOf": date})
	if rr.Code != 500 || !reflect.DeepEqual(before, st.Snapshot()) {
		t.Fatalf("failed write did not roll back: %d %s", rr.Code, rr.Body.String())
	}
}
