package boundaries

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Where and when a rule applies. Unlike the other conditions these are not
// figures compared against a threshold: folder is the project the session
// works in, hours the time of day in the operator's own time zone. A rule
// that sets them applies only inside its folders and hours; a rule that
// does not applies everywhere and always, as before.

// FieldFolder is the session's working directory: the rule applies when it
// is one of the rule's folders or inside one.
const FieldFolder Field = "folder"

// FieldHours is the time of day, with optional weekdays, in the operator's
// local time zone.
const FieldHours Field = "hours"

// listSeparator separates several folders or several windows in one value.
const listSeparator = ","

// userHomeDir expands a leading ~ in a folder; a variable so tests need no
// real home.
var userHomeDir = os.UserHomeDir

// parseFolders reads one or more project folders, separated by commas.
// Each must be absolute or start with ~, and may be a glob, as an
// allocation rule's dir is ("~/clients/*"). It is cleaned, so a trailing
// slash changes nothing. Symlinks are not resolved and case matters: a
// folder matches as written.
func parseFolders(op Op, value string) ([]string, error) {
	if op != "" {
		return nil, fmt.Errorf("folder takes a path, not a comparator, e.g. \"~/work/acme\"")
	}
	var out []string
	for _, raw := range strings.Split(value, listSeparator) {
		p := strings.TrimSpace(raw)
		if p == "" {
			return nil, fmt.Errorf("%q names an empty folder", value)
		}
		if p == "~" || strings.HasPrefix(p, "~/") {
			// A process with no home (the team server validating an
			// organisation's rules) keeps the ~: it matches no directory
			// there, and expands on each member's machine.
			if home, err := userHomeDir(); err == nil && home != "" {
				p = filepath.Join(home, strings.TrimPrefix(p[1:], "/"))
			}
			out = append(out, filepath.Clean(p))
			continue
		}
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("folder %q is not absolute (start it with / or ~/)", raw)
		}
		if _, err := filepath.Match(p, ""); err != nil {
			return nil, fmt.Errorf("folder %q is not a valid glob", raw)
		}
		out = append(out, filepath.Clean(p))
	}
	return out, nil
}

// inFolder reports whether dir, or a directory it lies under, matches
// folder: the same reading as an allocation rule's dir.
func inFolder(dir, folder string) bool {
	for d := dir; ; {
		if ok, _ := filepath.Match(folder, d); ok {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

// matchFolder is the first of folders that holds dir; dir is cleaned
// first. An unknown or relative dir matches nothing.
func matchFolder(folders []string, dir string) (string, bool) {
	if dir == "" || !filepath.IsAbs(dir) {
		return "", false
	}
	dir = filepath.Clean(dir)
	for _, f := range folders {
		if inFolder(dir, f) {
			return f, true
		}
	}
	return "", false
}

// HoursWindow is one stretch of time on chosen weekdays. A window whose
// end is before its start crosses midnight, and belongs to the day it
// starts on: "fri 22:00-06:00" runs from Friday night to Saturday morning.
type HoursWindow struct {
	// Days holds the weekdays the window starts on; all seven when none
	// were named.
	Days [7]bool
	// Start and End are minutes after midnight; End is exclusive and may
	// be 1440 (24:00).
	Start, End int
}

// crossesMidnight reports whether the window runs into the next day.
func (w HoursWindow) crossesMidnight() bool { return w.End < w.Start }

// Holds reports whether t, in its own location, falls inside the window.
func (w HoursWindow) Holds(t time.Time) bool {
	minute := t.Hour()*60 + t.Minute()
	day := t.Weekday()
	if !w.crossesMidnight() {
		return w.Days[day] && minute >= w.Start && minute < w.End
	}
	yesterday := (day + 6) % 7
	return (w.Days[day] && minute >= w.Start) || (w.Days[yesterday] && minute < w.End)
}

// String renders the window as it is written in config.
func (w HoursWindow) String() string {
	var parts []string
	if days := daysString(w.Days); days != "" {
		parts = append(parts, days)
	}
	if w.Start != 0 || w.End != minutesPerDay {
		parts = append(parts, clock(w.Start)+"-"+clock(w.End))
	}
	return strings.Join(parts, " ")
}

const minutesPerDay = 24 * 60

var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// parseHours reads one or more windows, separated by commas: an optional
// weekday or weekday range, then a time range in 24-hour clock, e.g.
// "09:00-18:00", "mon-fri 09:00-18:00", "22:00-06:00" or "sat-sun".
func parseHours(op Op, value string) ([]HoursWindow, error) {
	if op != "" {
		return nil, fmt.Errorf("hours takes time windows, not a comparator, e.g. \"mon-fri 09:00-18:00\"")
	}
	var out []HoursWindow
	for _, raw := range strings.Split(value, listSeparator) {
		w, err := parseHoursWindow(strings.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

func parseHoursWindow(s string) (HoursWindow, error) {
	fields := strings.Fields(strings.ToLower(s))
	if len(fields) == 0 || len(fields) > 2 {
		return HoursWindow{}, fmt.Errorf("window %q: write it as [weekdays] HH:MM-HH:MM, e.g. \"mon-fri 09:00-18:00\"", s)
	}
	w := HoursWindow{Start: 0, End: minutesPerDay}
	for d := range w.Days {
		w.Days[d] = true
	}
	daysPart, timePart := "", fields[len(fields)-1]
	switch {
	case len(fields) == 2:
		daysPart = fields[0]
	case !strings.ContainsAny(timePart, "0123456789"):
		// Weekdays alone: the whole of each day.
		daysPart, timePart = timePart, ""
	}
	if daysPart != "" {
		days, err := parseDays(daysPart)
		if err != nil {
			return HoursWindow{}, fmt.Errorf("window %q: %w", s, err)
		}
		w.Days = days
	}
	if timePart == "" {
		return w, nil
	}
	from, to, ok := strings.Cut(timePart, "-")
	if !ok {
		return HoursWindow{}, fmt.Errorf("window %q: %q needs a start and an end, e.g. 09:00-18:00", s, timePart)
	}
	start, err := parseClock(from, false)
	if err != nil {
		return HoursWindow{}, fmt.Errorf("window %q: %w", s, err)
	}
	end, err := parseClock(to, true)
	if err != nil {
		return HoursWindow{}, fmt.Errorf("window %q: %w", s, err)
	}
	if start == end {
		return HoursWindow{}, fmt.Errorf("window %q starts and ends at the same time; for the whole day leave the times out", s)
	}
	w.Start, w.End = start, end
	return w, nil
}

// parseClock reads HH:MM on a 24-hour clock. 24:00 is allowed as an end.
func parseClock(s string, end bool) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || len(h) == 0 || len(h) > 2 || len(m) != 2 || !digits(h) || !digits(m) {
		return 0, fmt.Errorf("%q is not a time like 09:00", s)
	}
	hour, minute := atoi(h), atoi(m)
	if hour == 24 && minute == 0 && end {
		return minutesPerDay, nil
	}
	if hour > 23 || minute > 59 {
		return 0, fmt.Errorf("%q is not a time of day (00:00 to 23:59)", s)
	}
	return hour*60 + minute, nil
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

// parseDays reads weekdays: one ("sat"), a range ("mon-fri"; a range may
// wrap the week, as "fri-mon" does), or several joined by + ("mon+wed+fri",
// "mon-wed+sat").
func parseDays(s string) ([7]bool, error) {
	var out [7]bool
	for _, part := range strings.Split(s, "+") {
		from, to, isRange := strings.Cut(part, "-")
		first, ok := dayIndex(from)
		last := first
		if ok && isRange {
			last, ok = dayIndex(to)
		}
		if !ok {
			return out, fmt.Errorf("%q is not a weekday (mon, tue, wed, thu, fri, sat, sun) or a range like mon-fri", part)
		}
		for d := first; ; d = (d + 1) % 7 {
			out[d] = true
			if d == last {
				break
			}
		}
	}
	return out, nil
}

func dayIndex(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 3 {
		return 0, false
	}
	for i, name := range fullDayNames {
		if strings.HasPrefix(name, s) {
			return i, true
		}
	}
	return 0, false
}

var fullDayNames = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

// daysString renders the days as runs, the week starting on Monday: "mon-fri",
// "sat-sun", "mon+wed-thu". It is "" when every day is set.
func daysString(days [7]bool) string {
	count := 0
	for _, on := range days {
		if on {
			count++
		}
	}
	if count == 7 || count == 0 {
		return ""
	}
	var runs []string
	for i := range 7 {
		d := (i + 1) % 7
		// A run starts on a set day whose day before is not set.
		if !days[d] || days[(d+6)%7] {
			continue
		}
		last := d
		for days[(last+1)%7] {
			last = (last + 1) % 7
		}
		if d == last {
			runs = append(runs, dayNames[d])
		} else {
			runs = append(runs, dayNames[d]+"-"+dayNames[last])
		}
	}
	return strings.Join(runs, "+")
}

func clock(minutes int) string { return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60) }

// matchHours is the first window holding t; a zero t is unknown and holds
// nothing.
func matchHours(windows []HoursWindow, t time.Time) (HoursWindow, bool) {
	if t.IsZero() {
		return HoursWindow{}, false
	}
	for _, w := range windows {
		if w.Holds(t) {
			return w, true
		}
	}
	return HoursWindow{}, false
}

func hoursString(windows []HoursWindow) string {
	parts := make([]string, len(windows))
	for i, w := range windows {
		parts[i] = w.String()
		if parts[i] == "" {
			parts[i] = "all day"
		}
	}
	return strings.Join(parts, ", ")
}
