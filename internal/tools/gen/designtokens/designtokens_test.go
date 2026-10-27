package designtokens

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/tools/gen"
)

const root = "../../../.."

const regenerate = "regenerate with: go generate ./internal/tools/gen/designtokens"

func load(t *testing.T) File {
	t.Helper()
	f, err := Load(filepath.Join(root, Source))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// tokens.css and its copies are what tokens.json says.
func TestGeneratedFilesAreCurrent(t *testing.T) {
	f := load(t)
	css, err := CSS(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range Outputs {
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, css) {
			t.Errorf("%s is stale; %s", path, regenerate)
		}
	}
}

// The brand README's token table is what tokens.json says.
func TestReadmeTableIsCurrent(t *testing.T) {
	f := load(t)
	doc, err := os.ReadFile(filepath.Join(root, Readme))
	if err != nil {
		t.Fatal(err)
	}
	table, err := Table(f)
	if err != nil {
		t.Fatal(err)
	}
	want, err := gen.ReplaceRegion(doc, RegionName, table)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(doc, want) {
		t.Errorf("%s region %q is stale; %s", Readme, RegionName, regenerate)
	}
}

// Every colour that names a ground meets its minimum contrast on it, in
// light and in dark.
func TestContrastMinimums(t *testing.T) {
	checks, err := Checks(load(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) < 10 {
		t.Fatalf("only %d contrast checks", len(checks))
	}
	for _, c := range checks {
		if c.Ratio < c.Min {
			t.Errorf("%s on %s (%s): %.2f:1, want at least %.1f:1", c.Token, c.On, c.Mode, c.Ratio, c.Min)
		}
	}
}

// The brand rules the palette rests on: white on lime is unreadable, ink
// on lime is not, and the logo's dark lime falls just short of 5:1, which
// is why links and text use the deeper one.
func TestBrandRatios(t *testing.T) {
	for _, tc := range []struct {
		fg, bg string
		want   float64
	}{
		{"#FFFFFF", "#C6F432", 1.28},
		{"#0E1220", "#C6F432", 14.55},
		{"#4D7C0F", "#FFFFFF", 4.99},
		{"#3F6B0B", "#FFFFFF", 6.33},
		{"#686D77", "#FFFFFF", 5.19},
		{"#000000", "#FFFFFF", 21},
	} {
		got, err := Contrast(tc.fg, tc.bg)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(got-tc.want) > 0.006 {
			t.Errorf("%s on %s: %.3f, want %.2f", tc.fg, tc.bg, got, tc.want)
		}
	}
	// A translucent colour is measured over its ground.
	got, err := Contrast("rgba(0, 0, 0, 0.5)", "#FFFFFF")
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := Contrast("#808080", "#FFFFFF"); math.Abs(got-want) > 0.05 {
		t.Errorf("half black on white: %.2f, want about %.2f", got, want)
	}
}

func TestCSSShape(t *testing.T) {
	css, err := CSS(load(t))
	if err != nil {
		t.Fatal(err)
	}
	s := string(css)
	for _, want := range []string{
		"DO NOT EDIT",
		"--tok-lime: #C6F432;",
		"--tok-bg: #FFFFFF;",
		"@media (prefers-color-scheme: dark) {\n  :root:not([data-theme=\"light\"]) {",
		"--tok-bg: #0E1220;",
		":root[data-theme=\"dark\"] {",
		"--tok-font-sans: system-ui",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("tokens.css lacks %q", want)
		}
	}
	// A token with one value is declared once, not again for dark.
	if n := strings.Count(s, "--tok-lime:"); n != 1 {
		t.Errorf("--tok-lime declared %d times", n)
	}
	// Dark values appear under the media query and the data-theme rule.
	if n := strings.Count(s, "--tok-bg: #0E1220;"); n != 2 {
		t.Errorf("dark --tok-bg declared %d times, want 2", n)
	}
}

func TestLoadRejects(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate":      `{"prefix":"tok","groups":[{"name":"a","tokens":[{"name":"x","value":"1px"},{"name":"x","value":"2px"}]}]}`,
		"no value":       `{"prefix":"tok","groups":[{"name":"a","tokens":[{"name":"x"}]}]}`,
		"half a pair":    `{"prefix":"tok","groups":[{"name":"a","tokens":[{"name":"x","light":"#FFFFFF"}]}]}`,
		"value and pair": `{"prefix":"tok","groups":[{"name":"a","tokens":[{"name":"x","value":"#FFFFFF","light":"#FFFFFF","dark":"#000000"}]}]}`,
		"unknown ground": `{"prefix":"tok","groups":[{"name":"a","tokens":[{"name":"x","value":"#FFFFFF","on":"nope","min":3}]}]}`,
		"ground no min":  `{"prefix":"tok","groups":[{"name":"a","tokens":[{"name":"x","value":"#FFFFFF"},{"name":"y","value":"#000000","on":"x"}]}]}`,
		"bad name":       `{"prefix":"tok","groups":[{"name":"a","tokens":[{"name":"X Y","value":"1px"}]}]}`,
		"no prefix":      `{"groups":[{"name":"a","tokens":[{"name":"x","value":"1px"}]}]}`,
		"unknown field":  `{"prefix":"tok","groups":[{"name":"a","tokens":[{"name":"x","value":"1px","colour":"#FFF"}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(body)); err == nil {
				t.Error("accepted")
			}
		})
	}
}
