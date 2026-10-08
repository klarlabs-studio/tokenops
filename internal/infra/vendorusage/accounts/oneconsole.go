package accounts

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Alibaba Cloud's consoles (Model Studio, Bailian, Qwen Cloud) answer
// through the OneConsole gateway, which nests the payload in envelopes and
// often as JSON inside a string. These helpers walk such an answer without
// committing to one schema, as CodexBar's OneConsoleJSON does
// (Sources/CodexBarCore/Providers/Shared/AliyunOneConsole/OneConsoleJSON.swift).

// decodeConsole parses a console answer and expands JSON held in strings.
func decodeConsole(body []byte) (any, bool) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, false
	}
	return expandJSON(v), true
}

// expandJSON replaces every string that is itself JSON with its value.
func expandJSON(v any) any {
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") {
			return t
		}
		var inner any
		if err := json.Unmarshal([]byte(s), &inner); err != nil {
			return t
		}
		return expandJSON(inner)
	case map[string]any:
		for k, x := range t {
			t[k] = expandJSON(x)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = expandJSON(x)
		}
		return t
	}
	return v
}

// walk visits every object in v, each before its descendants, keys in
// sorted order so the first match is deterministic, until visit says stop.
func walk(v any, visit func(map[string]any) bool) bool {
	switch t := v.(type) {
	case map[string]any:
		if visit(t) {
			return true
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if walk(t[k], visit) {
				return true
			}
		}
	case []any:
		for _, x := range t {
			if walk(x, visit) {
				return true
			}
		}
	}
	return false
}

// objectWith is the first object holding any of keys.
func objectWith(v any, keys ...string) map[string]any {
	var found map[string]any
	walk(v, func(m map[string]any) bool {
		for _, k := range keys {
			if _, ok := m[k]; ok {
				found = m
				return true
			}
		}
		return false
	})
	return found
}

// deepString is the first non-empty string under one of keys, keys in
// the caller's order at each object.
func deepString(v any, keys ...string) string {
	var out string
	walk(v, func(m map[string]any) bool {
		for _, k := range keys {
			if s := strOf(m[k]); s != "" {
				out = s
				return true
			}
		}
		return false
	})
	return out
}

// deepNumber is the first number under one of keys.
func deepNumber(v any, keys ...string) (float64, bool) {
	var (
		out float64
		ok  bool
	)
	walk(v, func(m map[string]any) bool {
		for _, k := range keys {
			if n, isNum := numOf(m[k]); isNum {
				out, ok = n, true
				return true
			}
		}
		return false
	})
	return out, ok
}

// field is the first of keys present in m with a value.
func field(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

// strOf is a trimmed string value, "" for anything else.
func strOf(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// numOf is a number or a numeric string (thousands separators dropped).
func numOf(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, !math.IsNaN(t) && !math.IsInf(t, 0)
	case string:
		f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(t), ",", ""), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

// boolOf reads true/false and the consoles' word forms of them.
func boolOf(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case float64:
		return t != 0, true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes", "active", "valid", "normal":
			return true, true
		case "false", "0", "no", "inactive", "invalid", "expired":
			return false, true
		}
	}
	return false, false
}

// timeOf reads Unix seconds or milliseconds (a number or numeric string),
// an ISO 8601 time, or "yyyy-MM-dd[ HH:mm[:ss]]" (UTC). Zero when absent.
func timeOf(v any) time.Time {
	if n, ok := numOf(v); ok {
		if n <= 0 {
			return time.Time{}
		}
		if n >= 1e12 {
			return time.UnixMilli(int64(n)).UTC()
		}
		return time.Unix(int64(n), 0).UTC()
	}
	s := strOf(v)
	if s == "" {
		return time.Time{}
	}
	if t := parseTime(s); !t.IsZero() {
		return t
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// mustJSON encodes a request parameter built from plain values.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// secTokenPatterns find the console's security token in its HTML shell,
// as CodexBar scrapes it (window.ALIYUN_CONSOLE_CONFIG.SEC_TOKEN).
var secTokenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`"secToken"\s*:\s*"([^"]+)"`),
	regexp.MustCompile(`"sec_token"\s*:\s*"([^"]+)"`),
	regexp.MustCompile(`secToken['"]?\s*[:=]\s*['"]([^'"]+)['"]`),
	regexp.MustCompile(`sec_token['"]?\s*[:=]\s*['"]([^'"]+)['"]`),
	regexp.MustCompile(`SEC_TOKEN['"]?\s*[:=]\s*['"]([^'"]+)['"]`),
}

// secTokenIn is the security token in a console page, "" for none.
func secTokenIn(html []byte) string {
	for _, re := range secTokenPatterns {
		if m := re.FindSubmatch(html); m != nil && len(m[1]) > 0 {
			return string(m[1])
		}
	}
	return ""
}
