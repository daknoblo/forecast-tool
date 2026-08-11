package docsite

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Shot describes one screenshot of the demo instance. Selector limits the
// capture to a single element; otherwise the whole page is captured.
type Shot struct {
	File        string `json:"file"`
	Path        string `json:"path"`
	Selector    string `json:"selector,omitempty"`
	FullPage    bool   `json:"fullPage"`
	Private     bool   `json:"private,omitempty"`
	Title       string `json:"-"`
	Description string `json:"-"`
}

// shotJob is the contract with tools/screenshots/capture.mjs.
type shotJob struct {
	BaseURL           string `json:"baseUrl"`
	OutDir            string `json:"outDir"`
	Width             int    `json:"width"`
	Height            int    `json:"height"`
	DeviceScaleFactor int    `json:"deviceScaleFactor"`
	Shots             []Shot `json:"shots"`
}

// DemoShots lists the screenshots shown in the gallery. Every section of the
// application appears at least once, so the gallery documents the whole tool.
func DemoShots(week int) []Shot {
	// Start one week early: the current week may not have a past day yet, and
	// without one the screenshot cannot show the "gebucht" markers.
	from := week - 1
	if from < 1 {
		from = 1
	}
	weekPath := fmt.Sprintf("/week/%d?weeks=3", from)
	return []Shot{
		{
			File: "dashboard.png", Path: "/", FullPage: true,
			Title:       "Dashboard",
			Description: "KPI-Kacheln, Auslastungs-Sankey, freie Kapazität, Budget- und Wochentabelle auf einen Blick.",
		},
		{
			File: "dashboard-sankey.png", Path: "/?sankey=fy", Selector: ".sankey-card",
			Title:       "Auslastung über das Fiskaljahr",
			Description: "Jede Spalte ist eine ISO-Woche. Bänder zeigen, wie sich die geplanten Stunden über die Projekte verteilen; Urlaubswochen nehmen die Projekte auf und geben sie wieder frei.",
		},
		{
			File: "forecast.png", Path: weekPath, FullPage: true,
			Title:       "Forecast-Raster",
			Description: "Projekte × Tage über mehrere Wochen, ein Stundenwert pro Tag. Vergangene Tage gelten als gebucht, ab heute als Forecast. Eingaben speichern sich automatisch.",
		},
		{
			File: "projects.png", Path: "/projects", FullPage: true,
			Title:       "Projekte",
			Description: "Budget, Übertrag aus früheren Fiskaljahren, verfügbares Restbudget, Burnrate und Burn-Down-Diagramm je Assignment.",
		},
		{
			File: "goal.png", Path: "/goal", FullPage: true,
			Title:       "Ziele & Kapazität",
			Description: "Fiskaljahresziel, Kapazitätsrechnung, Stundenfluss sowie Fortschrittsdiagramme je Halbjahr und Quartal.",
		},
		{
			File: "goal-flow.png", Path: "/goal", Selector: ".flow-wrap",
			Title:       "Stundenfluss",
			Description: "Projekte → Monate → Quartale → Halbjahre → Fiskaljahr. Die Farbe zeigt den Kalenderfortschritt, der kräftige Anteil die bereits gebuchten Stunden.",
		},
		{
			File: "settings.png", Path: "/settings", FullPage: true,
			Title:       "Einstellungen",
			Description: "Globale Werte und die Stundenrechnung des Fiskaljahres: Bruttostunden − Urlaub − Feiertage − Standard Tasks = Fiskaljahresziel.",
		},
		{
			File: "private.png", Path: "/", FullPage: true, Private: true,
			Title:       "Privater Modus",
			Description: "Ein Klick maskiert alle Projektnamen und Zahlen – auch in den Diagrammen, deren Spalten dann gleich hoch sind.",
		},
	}
}

// CaptureScreenshots renders the shot list against the running demo server by
// invoking the Playwright helper in tools/screenshots.
func CaptureScreenshots(script, baseURL, outDir string, shots []Shot) error {
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return err
	}
	abs, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}
	job := shotJob{
		BaseURL:           baseURL,
		OutDir:            abs,
		Width:             1440,
		Height:            1000,
		DeviceScaleFactor: 2,
		Shots:             shots,
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	jobFile := filepath.Join(abs, "shots.json")
	if err := os.WriteFile(jobFile, payload, 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(jobFile) }()

	cmd := exec.Command("node", script, jobFile) // #nosec G204 -- fixed helper script from this repository, no external input
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("playwright capture failed (run `npm ci` in %s): %w", filepath.Dir(script), err)
	}
	return nil
}
