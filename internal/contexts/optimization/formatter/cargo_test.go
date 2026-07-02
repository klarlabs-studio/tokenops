package formatter

import (
	"strings"
	"testing"
)

const cargoBuildRaw = `   Compiling libc v0.2.148
   Compiling proc-macro2 v1.0.66
   Compiling mycrate v0.1.0 (/home/user/mycrate)
warning: unused variable: ` + "`x`" + `
  --> src/main.rs:2:9
   |
2  |     let x = 5;
   |         ^ help: if this is intentional, prefix it with an underscore: ` + "`_x`" + `
   |
   = note: ` + "`#[warn(unused_variables)]`" + ` on by default
error[E0308]: mismatched types
  --> src/main.rs:10:5
   |
10 |     "hello"
   |     ^^^^^^^ expected ` + "`i32`" + `, found ` + "`&str`" + `
error: could not compile ` + "`mycrate`" + ` due to previous error
`

const cargoPassRaw = `   Compiling mycrate v0.1.0 (/home/user/mycrate)
    Finished dev [unoptimized + debuginfo] target(s) in 2.3s
`

func TestCargo_ErrorSurvivesEveryLevel(t *testing.T) {
	c := NewCargo()
	critical := []string{
		"error[E0308]: mismatched types",
		"error: could not compile `mycrate` due to previous error",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := c.Format([]byte(cargoBuildRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Fatalf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		for _, cl := range critical {
			if !strings.Contains(compact, cl) {
				t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, cl, compact)
			}
		}
	}
}

func TestCargo_BalancedDropsCompiling(t *testing.T) {
	c := NewCargo()
	res, _ := c.Format([]byte(cargoBuildRaw), LossBalanced)
	compact := string(res.Compact)
	if strings.Contains(compact, "Compiling") {
		t.Errorf("balanced kept Compiling progress:\n%s", compact)
	}
}

func TestCargo_AggressiveDropsWarnings(t *testing.T) {
	c := NewCargo()
	res, _ := c.Format([]byte(cargoBuildRaw), LossAggressive)
	compact := string(res.Compact)
	if strings.Contains(compact, "warning:") {
		t.Errorf("aggressive should drop advisory warnings:\n%s", compact)
	}
	if !strings.Contains(compact, "error[E0308]") {
		t.Errorf("aggressive dropped the error line:\n%s", compact)
	}
}

func TestCargo_MonotonicReduction(t *testing.T) {
	c := NewCargo()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := c.Format([]byte(cargoBuildRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestCargo_NonCargoFallsBackToGeneric(t *testing.T) {
	c := NewCargo()
	raw := "some random tool output\nhello world\nnothing to see here\n"
	res, ok := c.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestCargo_PassingBuildCompresses(t *testing.T) {
	c := NewCargo()
	res, ok := c.Format([]byte(cargoPassRaw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !res.CriticalKept {
		t.Fatal("CriticalKept=false")
	}
	if res.BytesAfter > res.BytesBefore {
		t.Errorf("passing build grew: %d > %d", res.BytesAfter, res.BytesBefore)
	}
}
