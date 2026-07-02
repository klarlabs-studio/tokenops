package formatter

import (
	"slices"
	"strings"
	"testing"
)

// dnfInstallRaw is a realistic `dnf install` transcript: the metadata-check
// preamble, the dependency-resolution table with "Installing:" /
// "Installing dependencies:" sections, the transaction summary, the download
// progress, the "Running transaction" steps, the post-transaction "Installed:"
// summary, and the terminal "Complete!".
const dnfInstallRaw = `Last metadata expiration check: 0:15:23 ago on Tue 01 Jul 2025 10:00:00 AM UTC.
Dependencies resolved.
================================================================================
 Package              Arch      Version            Repository          Size
================================================================================
Installing:
 libfoo               x86_64    1.0-1.el8          baseos             120 k
 libbar               x86_64    2.0-1.el8          appstream          200 k
Installing dependencies:
 libbaz               x86_64    3.0-1.el8          baseos              80 k
 libqux               x86_64    4.0-1.el8          baseos              60 k
 libquux              x86_64    5.0-1.el8          baseos              40 k

Transaction Summary
================================================================================
Install  5 Packages

Total download size: 500 k
Installed size: 1.5 M
Downloading Packages:
(1/5): libfoo-1.0-1.el8.x86_64.rpm              120 kB/s | 120 kB     00:01
(2/5): libbar-2.0-1.el8.x86_64.rpm              200 kB/s | 200 kB     00:01
Running transaction check
Running transaction test
Transaction test succeeded
Running transaction
  Preparing        :                                                      1/1
  Installing       : libbaz-3.0-1.el8.x86_64                              1/5
  Installing       : libfoo-1.0-1.el8.x86_64                              2/5
  Verifying        : libfoo-1.0-1.el8.x86_64                              1/5
  Verifying        : libbar-2.0-1.el8.x86_64                              2/5

Installed:
  libfoo.x86_64 1.0-1.el8          libbar.x86_64 2.0-1.el8
  libbaz.x86_64 3.0-1.el8          libqux.x86_64 4.0-1.el8
  libquux.x86_64 5.0-1.el8

Complete!
`

// dnfErrorRaw is an install that fails to locate a package.
const dnfErrorRaw = `Last metadata expiration check: 0:15:23 ago on Tue 01 Jul 2025.
Error: Unable to find a match: nonexistent
`

func TestDNF_CriticalSurvivesEveryLevel(t *testing.T) {
	d := NewDNF()
	cases := []struct {
		raw      string
		critical []string
	}{
		{
			raw: dnfInstallRaw,
			critical: []string{
				"Transaction Summary",
				"Install  5 Packages",
				"Installed:",
				"libfoo.x86_64 1.0-1.el8",
				"Complete!",
			},
		},
		{
			raw: dnfErrorRaw,
			critical: []string{
				"Error: Unable to find a match: nonexistent",
			},
		},
	}
	for _, tc := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := d.Format([]byte(tc.raw), level)
			if !ok {
				t.Fatalf("level=%s ok=false", level)
			}
			if !res.CriticalKept {
				t.Fatalf("level=%s CriticalKept=false", level)
			}
			compact := string(res.Compact)
			for _, c := range tc.critical {
				if !strings.Contains(compact, c) {
					t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, c, compact)
				}
			}
		}
	}
}

func TestDNF_BalancedDropsNoise(t *testing.T) {
	d := NewDNF()
	res, _ := d.Format([]byte(dnfInstallRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"Last metadata expiration",
		"Downloading Packages:",
		"120 kB/s",
		"Running transaction",
		"Verifying",
		"Preparing",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The transaction summary and terminal state must still be present.
	for _, keep := range []string{
		"Transaction Summary",
		"Install  5 Packages",
		"Installed:",
		"Complete!",
	} {
		if !strings.Contains(compact, keep) {
			t.Errorf("balanced dropped signal %q:\n%s", keep, compact)
		}
	}
}

func TestDNF_AggressiveCollapsesTable(t *testing.T) {
	d := NewDNF()
	res, _ := d.Format([]byte(dnfInstallRaw), LossAggressive)
	compact := string(res.Compact)
	// The indented resolution-table rows collapse to a count.
	if strings.Contains(compact, "baseos             120 k") {
		t.Errorf("aggressive should collapse the resolution table:\n%s", compact)
	}
	if !strings.Contains(compact, "(+") {
		t.Errorf("aggressive should emit a package count:\n%s", compact)
	}
	// The summary and "Installed:" entries must still be present.
	for _, keep := range []string{
		"Transaction Summary",
		"Install  5 Packages",
		"Installed:",
		"Complete!",
	} {
		if !strings.Contains(compact, keep) {
			t.Errorf("aggressive dropped signal %q:\n%s", keep, compact)
		}
	}
}

func TestDNF_MonotonicReduction(t *testing.T) {
	d := NewDNF()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := d.Format([]byte(dnfInstallRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestDNF_NonMatchingFallsBackToGeneric(t *testing.T) {
	d := NewDNF()
	raw := "Hello world\nThis is not package output\nJust some plain text\n"
	res, ok := d.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestDNF_AliasesIncludeYum(t *testing.T) {
	if !slices.Contains(NewDNF().Aliases(), "yum") {
		t.Errorf("expected Aliases() to contain %q, got %v", "yum", NewDNF().Aliases())
	}
}
