package formatter

import (
	"strings"
	"testing"
)

const nixFailRaw = `these 3 derivations will be built:
  /nix/store/aaa-foo.drv
  /nix/store/bbb-bar.drv
  /nix/store/ccc-baz.drv
building '/nix/store/aaa-foo.drv'...
foo> configuring
foo> compiling foo.c
foo> running tests
copying path '/nix/store/ddd-dep' from 'https://cache.nixos.org'...
building '/nix/store/bbb-bar.drv'...
bar> compiling bar.c
bar> undefined reference to symbol
error: builder for '/nix/store/bbb-bar.drv' failed with exit code 1
error: build of '/nix/store/bbb-bar.drv' failed
`

const nixOkRaw = `these 2 derivations will be built:
  /nix/store/aaa-foo.drv
  /nix/store/bbb-bar.drv
building '/nix/store/aaa-foo.drv'...
foo> compiling foo.c
foo> done
copying path '/nix/store/ccc-out' from 'https://cache.nixos.org'...
building '/nix/store/bbb-bar.drv'...
bar> compiling bar.c
bar> done
`

func TestNix_CriticalSurvivesEveryLevel(t *testing.T) {
	n := NewNix()
	critical := []string{
		"these 3 derivations will be built:",
		"error: builder for '/nix/store/bbb-bar.drv' failed with exit code 1",
		"error: build of '/nix/store/bbb-bar.drv' failed",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := n.Format([]byte(nixFailRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Fatalf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		for _, c := range critical {
			if !strings.Contains(compact, strings.TrimSpace(c)) {
				t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, c, compact)
			}
		}
	}
}

func TestNix_BalancedDropsNoise(t *testing.T) {
	n := NewNix()
	res, _ := n.Format([]byte(nixFailRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"copying path",
		"building '",
		"aaa-foo.drv", // the indented derivation listing under the plan summary
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The plan summary, the errors, and the wrapped build-log echo survive at
	// Balanced.
	for _, keep := range []string{
		"these 3 derivations will be built:",
		"error: build of '/nix/store/bbb-bar.drv' failed",
		"foo> compiling foo.c",
	} {
		if !strings.Contains(compact, keep) {
			t.Errorf("balanced dropped %q:\n%s", keep, compact)
		}
	}

	// Aggressive additionally drops the "foo> …" build-log echo, but keeps an
	// echo line that carries an error/failure signal.
	aggr, _ := n.Format([]byte(nixFailRaw), LossAggressive)
	aggrCompact := string(aggr.Compact)
	if strings.Contains(aggrCompact, "foo> compiling foo.c") {
		t.Errorf("aggressive kept build-log echo:\n%s", aggrCompact)
	}
}

func TestNix_MonotonicReduction(t *testing.T) {
	n := NewNix()
	for _, raw := range []string{nixFailRaw, nixOkRaw} {
		last := 1 << 30
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, _ := n.Format([]byte(raw), level)
			if res.BytesAfter > last {
				t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
			}
			last = res.BytesAfter
		}
	}
}

func TestNix_NonMatchingFallsBackToGeneric(t *testing.T) {
	n := NewNix()
	raw := "some unrelated log line\nanother line without build signal\n"
	res, ok := n.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
