// Package designtokens turns assets/brand/tokens.json, the one set of
// design tokens every TokenOps surface draws from, into CSS custom
// properties (assets/brand/tokens.css and the copy the menu bar embeds) and
// the token table in assets/brand/README.md. It also measures the WCAG
// contrast of every colour that names the ground it is used on; the tests
// fail when one falls below its minimum or a generated file is stale.
package designtokens

//go:generate go run go.klarlabs.de/tokenops/internal/tools/gen/tokensgen -root ../../../..

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Source is the token file, relative to the repository root.
const Source = "assets/brand/tokens.json"

// Readme holds the generated token table between region markers.
const Readme = "assets/brand/README.md"

// RegionName names the README's generated region.
const RegionName = "tokens"

// Outputs are the generated CSS files, relative to the repository root.
// Each is the same bytes: the menu bar is its own Go module and embeds only
// files under its frontend directory, so it carries a copy.
var Outputs = []string{
	"assets/brand/tokens.css",
	"apps/menubar/frontend/tokens.css",
}

// File is tokens.json.
type File struct {
	Description string  `json:"description"`
	Prefix      string  `json:"prefix"`
	Groups      []Group `json:"groups"`
}

// Group is a titled set of tokens, emitted in order.
type Group struct {
	Name   string  `json:"name"`
	About  string  `json:"about,omitempty"`
	Tokens []Token `json:"tokens"`
}

// Token is one custom property. It has either Value, the same in light and
// dark, or both Light and Dark. A colour with On names the token it is
// drawn on and Min the contrast ratio it must keep there.
type Token struct {
	Name  string  `json:"name"`
	Value string  `json:"value,omitempty"`
	Light string  `json:"light,omitempty"`
	Dark  string  `json:"dark,omitempty"`
	Use   string  `json:"use,omitempty"`
	On    string  `json:"on,omitempty"`
	Min   float64 `json:"min,omitempty"`
}

// Paired reports whether the token differs between light and dark.
func (t Token) Paired() bool { return t.Value == "" }

// In is the token's value in mode "light" or "dark".
func (t Token) In(mode string) string {
	switch {
	case !t.Paired():
		return t.Value
	case mode == "dark":
		return t.Dark
	default:
		return t.Light
	}
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Load reads and checks a token file.
func Load(path string) (File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	f, err := Parse(b)
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Parse decodes a token file and checks that every name is unique and
// well formed, every token has one value or a light and dark pair, and
// every ground a colour names exists.
func Parse(b []byte) (File, error) {
	var f File
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return File{}, err
	}
	if !namePattern.MatchString(f.Prefix) {
		return File{}, fmt.Errorf("prefix %q: want lowercase letters, digits and dashes", f.Prefix)
	}
	seen := map[string]bool{}
	for _, g := range f.Groups {
		for _, t := range g.Tokens {
			if err := checkToken(t, seen); err != nil {
				return File{}, err
			}
			seen[t.Name] = true
		}
	}
	for _, t := range f.all() {
		if t.On == "" {
			continue
		}
		if !seen[t.On] {
			return File{}, fmt.Errorf("token %q: ground %q is not a token", t.Name, t.On)
		}
		if t.Min <= 1 {
			return File{}, fmt.Errorf("token %q: names a ground without a minimum contrast", t.Name)
		}
	}
	return f, nil
}

func checkToken(t Token, seen map[string]bool) error {
	if !namePattern.MatchString(t.Name) {
		return fmt.Errorf("token %q: want lowercase letters, digits and dashes", t.Name)
	}
	if seen[t.Name] {
		return fmt.Errorf("token %q: declared twice", t.Name)
	}
	pair := t.Light != "" || t.Dark != ""
	switch {
	case t.Value != "" && pair:
		return fmt.Errorf("token %q: has a value and a light/dark pair", t.Name)
	case t.Value == "" && (t.Light == "" || t.Dark == ""):
		return fmt.Errorf("token %q: needs a value, or both light and dark", t.Name)
	}
	return nil
}

func (f File) all() []Token {
	var out []Token
	for _, g := range f.Groups {
		out = append(out, g.Tokens...)
	}
	return out
}

func (f File) lookup(name string) (Token, bool) {
	for _, t := range f.all() {
		if t.Name == name {
			return t, true
		}
	}
	return Token{}, false
}

func (f File) prop(name string) string { return "--" + f.Prefix + "-" + name }

// CSS renders the custom properties: every token on :root in its light (or
// only) value, the dark values under prefers-color-scheme unless the page
// pins data-theme="light", and again under data-theme="dark".
func CSS(f File) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("/* Code generated from " + Source + " by internal/tools/gen/tokensgen; DO NOT EDIT.\n")
	b.WriteString("   Regenerate: go generate ./internal/tools/gen/designtokens\n")
	b.WriteString("   The same file is written to " + strings.Join(Outputs, " and ") + ".\n\n")
	for _, line := range wrap("TokenOps design tokens: electric lime on ink. Light is plain white, dark is plain ink. Lime fills carry ink text, never white; on a light ground lime as text, a link or a thin stroke is the dark lime. Pin a mode with data-theme=\"light\" or \"dark\" on the root element.", 72) {
		b.WriteString("   " + line + "\n")
	}
	b.WriteString("*/\n\n:root {\n  color-scheme: light dark;\n")
	for _, g := range f.Groups {
		fmt.Fprintf(&b, "\n  /* %s */\n", g.Name)
		for _, t := range g.Tokens {
			fmt.Fprintf(&b, "  %s: %s;\n", f.prop(t.Name), t.In("light"))
		}
	}
	b.WriteString("}\n\n")
	dark := darkBlock(f, "    ")
	b.WriteString("@media (prefers-color-scheme: dark) {\n  :root:not([data-theme=\"light\"]) {\n")
	b.WriteString(dark)
	b.WriteString("  }\n}\n\n")
	b.WriteString(":root[data-theme=\"dark\"] {\n  color-scheme: dark;\n")
	b.WriteString(darkBlock(f, "  "))
	b.WriteString("}\n\n:root[data-theme=\"light\"] {\n  color-scheme: light;\n}\n")
	return b.Bytes(), nil
}

func darkBlock(f File, indent string) string {
	var b strings.Builder
	for _, g := range f.Groups {
		first := true
		for _, t := range g.Tokens {
			if !t.Paired() || t.Light == t.Dark {
				continue
			}
			if first {
				fmt.Fprintf(&b, "%s/* %s */\n", indent, g.Name)
				first = false
			}
			fmt.Fprintf(&b, "%s%s: %s;\n", indent, f.prop(t.Name), t.Dark)
		}
	}
	return b.String()
}

func wrap(s string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		if line != "" && len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	return append(lines, line)
}

// Check is one measured contrast.
type Check struct {
	Token, On, Mode string
	Ratio, Min      float64
}

// Checks measures every colour that names a ground, in light and dark.
// A token the same in both modes on a ground the same in both is measured
// once, as "both".
func Checks(f File) ([]Check, error) {
	var out []Check
	for _, t := range f.all() {
		if t.On == "" {
			continue
		}
		ground, _ := f.lookup(t.On)
		modes := []string{"light", "dark"}
		if t.In("light") == t.In("dark") && ground.In("light") == ground.In("dark") {
			modes = []string{"both"}
		}
		for _, mode := range modes {
			r, err := Contrast(t.In(mode), ground.In(mode))
			if err != nil {
				return nil, fmt.Errorf("token %q on %q: %w", t.Name, t.On, err)
			}
			out = append(out, Check{Token: t.Name, On: t.On, Mode: mode, Ratio: r, Min: t.Min})
		}
	}
	return out, nil
}

// Table renders the colour tokens as a Markdown table for the README:
// each value in light and dark and, where a colour names its ground, the
// contrast measured there.
func Table(f File) (string, error) {
	checks, err := Checks(f)
	if err != nil {
		return "", err
	}
	byToken := map[string][]Check{}
	for _, c := range checks {
		byToken[c.Token] = append(byToken[c.Token], c)
	}
	var b strings.Builder
	b.WriteString("| Token | Light | Dark | Contrast | Use |\n|---|---|---|---|---|\n")
	for _, g := range f.Groups {
		for _, t := range g.Tokens {
			if !isColour(t.In("light")) {
				continue
			}
			fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %s | %s |\n", f.prop(t.Name), t.In("light"), t.In("dark"), ratios(byToken[t.Name]), t.Use)
		}
	}
	return b.String(), nil
}

func ratios(cs []Check) string {
	if len(cs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		r := math.Floor(c.Ratio*100) / 100
		if c.Mode == "both" {
			parts = append(parts, fmt.Sprintf("%.2f:1", r))
		} else {
			parts = append(parts, fmt.Sprintf("%s %.2f:1", c.Mode, r))
		}
	}
	return strings.Join(parts, ", ") + fmt.Sprintf(" on `%s` (min %s)", cs[0].On, strconv.FormatFloat(cs[0].Min, 'f', -1, 64))
}

func isColour(v string) bool {
	_, err := parseColour(v)
	return err == nil
}

// Contrast is the WCAG 2.x contrast ratio of fg drawn on bg. A
// translucent fg is composited over bg first; bg must be opaque.
func Contrast(fg, bg string) (float64, error) {
	f, err := parseColour(fg)
	if err != nil {
		return 0, err
	}
	g, err := parseColour(bg)
	if err != nil {
		return 0, err
	}
	if g.a < 1 {
		return 0, fmt.Errorf("ground %q is translucent", bg)
	}
	for i := range f.c {
		f.c[i] = f.c[i]*f.a + g.c[i]*(1-f.a)
	}
	l1, l2 := luminance(f.c), luminance(g.c)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05), nil
}

type rgba struct {
	c [3]float64 // 0–255
	a float64
}

var rgbaPattern = regexp.MustCompile(`^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*(?:,\s*([0-9.]+)\s*)?\)$`)

func parseColour(s string) (rgba, error) {
	if len(s) == 7 && s[0] == '#' {
		v, err := strconv.ParseUint(s[1:], 16, 32)
		if err != nil {
			return rgba{}, fmt.Errorf("colour %q: %w", s, err)
		}
		return rgba{c: [3]float64{float64(v >> 16), float64(v >> 8 & 0xff), float64(v & 0xff)}, a: 1}, nil
	}
	m := rgbaPattern.FindStringSubmatch(s)
	if m == nil {
		return rgba{}, fmt.Errorf("%q is not a #RRGGBB or rgba() colour", s)
	}
	var out rgba
	for i := 0; i < 3; i++ {
		v, _ := strconv.Atoi(m[i+1])
		if v > 255 {
			return rgba{}, fmt.Errorf("colour %q: channel over 255", s)
		}
		out.c[i] = float64(v)
	}
	out.a = 1
	if m[4] != "" {
		a, err := strconv.ParseFloat(m[4], 64)
		if err != nil || a < 0 || a > 1 {
			return rgba{}, fmt.Errorf("colour %q: alpha out of range", s)
		}
		out.a = a
	}
	return out, nil
}

func luminance(c [3]float64) float64 {
	lin := func(v float64) float64 {
		v /= 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c[0]) + 0.7152*lin(c[1]) + 0.0722*lin(c[2])
}
