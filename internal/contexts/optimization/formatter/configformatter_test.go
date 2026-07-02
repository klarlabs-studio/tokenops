package formatter

import (
	"strings"
	"testing"
)

func mustConfig(t *testing.T, spec ConfigSpec) *ConfigFormatter {
	t.Helper()
	f, err := NewConfigFormatter(spec)
	if err != nil {
		t.Fatalf("NewConfigFormatter: %v", err)
	}
	return f
}

const mytoolRaw = `INFO starting mytool v1.2
DEBUG loading config from /etc/mytool.conf
DEBUG resolved 12 modules
processing batch 1
ERROR failed to connect to db: timeout
processing batch 2
INFO done in 4s
`

func TestConfigFormatter_DropsNoiseKeepsCritical(t *testing.T) {
	f := mustConfig(t, ConfigSpec{
		Command:  "mytool",
		Critical: []string{`(?i)error`},
		Drop: map[LossLevel][]string{
			LossBalanced:   {`^DEBUG `},
			LossAggressive: {`^INFO `},
		},
	})

	// Balanced: DEBUG gone, INFO kept, ERROR kept.
	bal, ok := f.Format([]byte(mytoolRaw), LossBalanced)
	if !ok || !bal.CriticalKept {
		t.Fatalf("balanced ok=%v criticalKept=%v", ok, bal.CriticalKept)
	}
	b := string(bal.Compact)
	if strings.Contains(b, "DEBUG ") {
		t.Errorf("balanced should drop DEBUG:\n%s", b)
	}
	if !strings.Contains(b, "INFO starting") {
		t.Errorf("balanced should keep INFO:\n%s", b)
	}
	if !strings.Contains(b, "ERROR failed to connect") {
		t.Errorf("critical ERROR dropped:\n%s", b)
	}

	// Aggressive: INFO also gone, ERROR still kept.
	agg, _ := f.Format([]byte(mytoolRaw), LossAggressive)
	a := string(agg.Compact)
	if strings.Contains(a, "INFO ") {
		t.Errorf("aggressive should drop INFO:\n%s", a)
	}
	if !strings.Contains(a, "ERROR failed to connect") {
		t.Errorf("aggressive dropped critical ERROR:\n%s", a)
	}
	if agg.BytesAfter > bal.BytesAfter {
		t.Errorf("aggressive (%d) should not exceed balanced (%d)", agg.BytesAfter, bal.BytesAfter)
	}
}

// The key safety property: even if a user's DROP rule also matches a line
// they marked CRITICAL, the critical rule wins — the line survives, because
// CriticalLine is checked before the drop rules and enforceCritical guards
// the result.
func TestConfigFormatter_CriticalWinsOverDrop(t *testing.T) {
	f := mustConfig(t, ConfigSpec{
		Command:  "mytool",
		Critical: []string{`ERROR`},
		Drop: map[LossLevel][]string{
			// Overly broad drop rule that also matches the ERROR line.
			LossBalanced: {`connect`, `.`},
		},
	})
	res, ok := f.Format([]byte(mytoolRaw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(string(res.Compact), "ERROR failed to connect") {
		t.Errorf("critical line must survive an overbroad drop rule:\n%s", res.Compact)
	}
}

func TestConfigFormatter_ConservativeOnlyScrubs(t *testing.T) {
	f := mustConfig(t, ConfigSpec{
		Command:  "mytool",
		Critical: []string{`ERROR`},
		Drop:     map[LossLevel][]string{LossBalanced: {`^DEBUG `}},
	})
	res, _ := f.Format([]byte(mytoolRaw), LossConservative)
	if !strings.Contains(string(res.Compact), "DEBUG loading") {
		t.Errorf("conservative must not apply drop rules:\n%s", res.Compact)
	}
}

func TestConfigFormatter_InvalidRegexRejected(t *testing.T) {
	_, err := NewConfigFormatter(ConfigSpec{Command: "x", Critical: []string{`(`}})
	if err == nil {
		t.Error("invalid regex should be rejected at construction")
	}
}

func TestConfigFormatter_EmptyCommandRejected(t *testing.T) {
	if _, err := NewConfigFormatter(ConfigSpec{Command: "  "}); err == nil {
		t.Error("empty command should be rejected")
	}
}

func TestConfigFormatter_RegistryDispatchAndAlias(t *testing.T) {
	f := mustConfig(t, ConfigSpec{
		Command:  "mytool",
		Aliases:  []string{"mt"},
		Critical: []string{`ERROR`},
		Drop:     map[LossLevel][]string{LossBalanced: {`^DEBUG `}},
	})
	reg := NewRegistry(LossPolicy{Default: LossBalanced}, f)
	for _, tok := range []string{"mytool", "mt"} {
		res, handled := reg.Format([]string{tok}, []byte(mytoolRaw))
		if !handled {
			t.Errorf("token %q should be handled by the config formatter", tok)
		}
		if strings.Contains(string(res.Compact), "DEBUG ") {
			t.Errorf("token %q: DEBUG not dropped:\n%s", tok, res.Compact)
		}
	}
}

// A config formatter must not hijack the proxy content-sniff plane.
func TestConfigFormatter_ExcludedFromSniff(t *testing.T) {
	f := mustConfig(t, ConfigSpec{
		Command:  "mytool",
		Critical: []string{`ERROR`},
		Drop:     map[LossLevel][]string{LossAggressive: {`.`}}, // would eat everything
	})
	reg := NewRegistry(LossPolicy{Default: LossAggressive}, NewGit(), f)
	// Sniff over git-status content: the config formatter must be skipped,
	// so git wins (or generic), never the everything-dropping config rule.
	_, cmd := reg.FormatSniff([]byte(gitStatusRaw), LossAggressive)
	if cmd == "mytool" {
		t.Error("config formatter must be excluded from content sniffing")
	}
}
