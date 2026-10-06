package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// alertSteps are the shares left at which a window alerts: low, nearly
// gone, used up. Each alerts once on the way down.
var alertSteps = []float64{20, 5, 0}

// note is one desktop notification.
type note struct {
	Title, Body string
}

// alerter turns successive glances into notifications: a window crossing
// a step on the way down, and one that had alerted coming back after its
// reset. It remembers each window's step between readings, and stays
// quiet on the first reading so an app opened at login does not repeat
// what was already true.
type alerter struct {
	steps  map[string]int
	primed bool
}

// measure is one window, or a spend limit, as an alert sees it.
type measure struct {
	key, words string
	used       float64
	resetsIn   string
	pace       *pace
}

// observe reads a glance and returns what to notify about.
func (a *alerter) observe(raw json.RawMessage) []note {
	var g glanceView
	if json.Unmarshal(raw, &g) != nil || g.PlanHeadroom.Error != "" {
		return nil
	}
	if a.steps == nil {
		a.steps = map[string]int{}
	}
	var out []note
	for _, m := range measures(g) {
		step, prev := stepOf(m.used), a.steps[m.key]
		a.steps[m.key] = step
		switch {
		case !a.primed:
		case step > prev:
			out = append(out, warning(m, step))
		case step == 0 && prev > 0:
			out = append(out, note{Title: m.words + " has reset", Body: fmt.Sprintf("%.0f%% left.", left(m.used))})
		}
	}
	a.primed = true
	return out
}

// measures lists every window of every plan, and the spend limit of a
// plan the vendor reports no window for.
func measures(g glanceView) []measure {
	var out []measure
	for _, r := range g.PlanHeadroom.Reports {
		name := shortName(r.Provider)
		for _, w := range r.Windows {
			out = append(out, measure{
				key: r.Provider + "/" + w.Name + "/" + w.VendorLabel, words: name + " " + windowWords(w.Name),
				used: w.UsedPct, resetsIn: w.ResetsIn, pace: w.Pace,
			})
		}
		if len(r.Windows) == 0 && r.SpendLimitUSD > 0 {
			out = append(out, measure{key: r.Provider + "/spend", words: name + " spend limit", used: r.SpendPct})
		}
	}
	return out
}

// stepOf is how many alert steps a share used has crossed.
func stepOf(used float64) int {
	n := 0
	for _, s := range alertSteps {
		if left(used) <= s {
			n++
		}
	}
	return n
}

// warning words a window that has crossed a step.
func warning(m measure, step int) note {
	resets := ""
	if m.resetsIn != "" {
		resets = "resets in " + humanDuration(m.resetsIn) + "."
	}
	if step == len(alertSteps) {
		return note{Title: m.words + " is used up", Body: capitalize(resets)}
	}
	body := resets
	if p := m.pace; p != nil && p.Status == "ahead" && !p.LastsToReset && p.RunsOutInNs > 0 {
		body = strings.TrimSuffix(fmt.Sprintf("Runs out in %s at this pace; %s", humanDuration(time.Duration(p.RunsOutInNs).String()), resets), "; ")
	}
	return note{Title: fmt.Sprintf("%s: %.0f%% left", m.words, math.Floor(left(m.used))), Body: capitalize(body)}
}

// windowWords names a window in a sentence: "weekly window".
func windowWords(name string) string {
	if model, ok := strings.CutPrefix(name, "week ("); ok {
		return strings.TrimSuffix(model, ")") + " weekly window"
	}
	switch name {
	case "5h":
		return "session window"
	case "week":
		return "weekly window"
	case "day":
		return "daily window"
	case "month":
		return "monthly window"
	}
	if strings.HasSuffix(name, "limit") {
		return name
	}
	return name + " window"
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// settings are the menu bar's own preferences.
type settings struct {
	// Alerts is whether windows notify; nil is on.
	Alerts *bool `json:"alerts,omitempty"`
}

func (s settings) alertsOn() bool { return s.Alerts == nil || *s.Alerts }

// settingsPath is where the menu bar keeps its preferences.
func settingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "TokenOps", "menubar.json"), nil
}

// loadSettings reads the preferences; a missing or unreadable file is the
// defaults.
func loadSettings(path string) settings {
	var s settings
	b, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func saveSettings(path string, s settings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
