package docsite

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	Locale            string `json:"locale"`
}

// DemoShots captures every discovered navigation page and dashboard view.
// Only optional close-ups and the private-mode example need explicit entries.
func DemoShots(pages []Page) []Shot {
	shots := make([]Shot, 0, len(pages)+3)
	for _, p := range pages {
		file := strings.TrimSuffix(p.File, ".html") + ".png"
		if p.URL == "/" {
			file = "dashboard.png"
		}
		shots = append(shots, Shot{File: file, Path: p.URL, FullPage: true, Title: p.Title})
	}
	return append(shots, []Shot{
		{
			File: "dashboard-sankey.png", Path: "/?sankey=fy", Selector: ".sankey-card",
			Title:       "Fiscal-year utilization",
			Description: "Project hours across the fiscal year, including vacation weeks.",
		},
		{
			File: "goal-flow.png", Path: "/goal", Selector: ".flow-wrap",
			Title:       "Hours flow",
			Description: "Projects, months, quarters and fiscal-year progress.",
		},
		{
			File: "private.png", Path: "/", FullPage: true, Private: true,
			Title:       "Private mode",
			Description: "Presentation mode replaces the displayed figures with fictional sample data.",
		},
	}...)
}

// CaptureScreenshots renders the shot list against the running demo server by
// invoking the Playwright helper in tools/screenshots.
func CaptureScreenshots(script, baseURL, outDir string, shots []Shot, language string) error {
	seen := map[string]bool{}
	for _, shot := range shots {
		if filepath.Base(shot.File) != shot.File || !strings.HasSuffix(shot.File, ".png") || seen[shot.File] {
			return fmt.Errorf("invalid or duplicate screenshot filename: %q", shot.File)
		}
		seen[shot.File] = true
	}
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
		Locale:            "de-DE",
	}
	if language == "en" {
		job.Locale = "en-GB"
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
