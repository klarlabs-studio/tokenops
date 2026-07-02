package formatter

import (
	"slices"
	"strings"
	"testing"
)

// aptInstallRaw is a realistic `apt-get install` transcript: the index-read
// preamble, per-repository fetch chatter, the NEW-packages header with an
// indented listing body, the change summary, an advisory warning, and the
// per-package unpack/setup progress.
const aptInstallRaw = `Reading package lists...
Building dependency tree...
Reading state information...
Get:1 http://archive.ubuntu.com/ubuntu focal/main amd64 libfoo amd64 1.0 [12.3 kB]
Hit:2 http://archive.ubuntu.com/ubuntu focal InRelease
Ign:3 http://ppa.launchpad.net/foo/ppa/ubuntu focal InRelease
Get:4 http://archive.ubuntu.com/ubuntu focal/main amd64 libbar amd64 2.0 [45.6 kB]
Fetched 57.9 kB in 1s (58.0 kB/s)
W: Some index files failed to download.
The following NEW packages will be installed:
  libfoo libbar libbaz libqux
  libquux
0 upgraded, 5 newly installed, 0 to remove and 2 not upgraded.
Preparing to unpack .../libfoo_1.0_amd64.deb ...
Unpacking libfoo (1.0) ...
Unpacking libbar (2.0) ...
Setting up libfoo (1.0) ...
Setting up libbar (2.0) ...
Processing triggers for man-db (2.9.1-1) ...
`

// aptErrorRaw is an install that fails to locate a package.
const aptErrorRaw = `Reading package lists...
Building dependency tree...
Reading state information...
E: Unable to locate package nonexistent
`

func TestApt_CriticalSurvivesEveryLevel(t *testing.T) {
	a := NewApt()
	cases := []struct {
		raw      string
		critical []string
	}{
		{
			raw: aptInstallRaw,
			critical: []string{
				"0 upgraded, 5 newly installed, 0 to remove and 2 not upgraded.",
				"The following NEW packages will be installed:",
			},
		},
		{
			raw: aptErrorRaw,
			critical: []string{
				"E: Unable to locate package nonexistent",
			},
		},
	}
	for _, tc := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := a.Format([]byte(tc.raw), level)
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

func TestApt_BalancedDropsFetchNoise(t *testing.T) {
	a := NewApt()
	res, _ := a.Format([]byte(aptInstallRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"Get:1 http://archive.ubuntu.com",
		"Hit:2 http://archive.ubuntu.com",
		"Reading package lists",
		"Building dependency tree",
		"Fetched 57.9 kB",
		"Unpacking libfoo",
		"Setting up libfoo",
		"Processing triggers for",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The change summary must still be present.
	if !strings.Contains(compact, "0 upgraded, 5 newly installed") {
		t.Errorf("balanced dropped the change summary:\n%s", compact)
	}
}

func TestApt_AggressiveDropsWarningsAndCollapsesList(t *testing.T) {
	a := NewApt()
	res, _ := a.Format([]byte(aptInstallRaw), LossAggressive)
	compact := string(res.Compact)
	if strings.Contains(compact, "W: Some index files") {
		t.Errorf("aggressive should drop W: warnings:\n%s", compact)
	}
	if strings.Contains(compact, "libbaz libqux") {
		t.Errorf("aggressive should collapse the NEW-packages listing:\n%s", compact)
	}
	if !strings.Contains(compact, "(+5 packages)") {
		t.Errorf("aggressive should emit a package count:\n%s", compact)
	}
	// The header and change summary must still be present.
	if !strings.Contains(compact, "The following NEW packages will be installed:") {
		t.Errorf("aggressive dropped the NEW-packages header:\n%s", compact)
	}
	if !strings.Contains(compact, "0 upgraded, 5 newly installed") {
		t.Errorf("aggressive dropped the change summary:\n%s", compact)
	}
}

func TestApt_MonotonicReduction(t *testing.T) {
	a := NewApt()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := a.Format([]byte(aptInstallRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestApt_NonAptFallsBackToGeneric(t *testing.T) {
	a := NewApt()
	raw := "Hello world\nThis is not package output\nJust some plain text\n"
	res, ok := a.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestApt_AliasesIncludeAptGet(t *testing.T) {
	if !slices.Contains(NewApt().Aliases(), "apt-get") {
		t.Errorf("expected Aliases() to contain %q, got %v", "apt-get", NewApt().Aliases())
	}
}
