package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestActivityIsStableWhenRedirected(t *testing.T) {
	var out bytes.Buffer
	a := startActivityMode(&out, "Checking credentials", false)
	a.success("Credentials verified")
	if got, want := out.String(), "Checking credentials...\n✓ Credentials verified\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestActivityAnimatesAndClearsOneTerminalLine(t *testing.T) {
	var out bytes.Buffer
	a := startActivityMode(&out, "Checking credentials", true)
	time.Sleep(90 * time.Millisecond)
	a.failure("Verification failed")
	got := out.String()
	if !strings.Contains(got, "\r\x1b[2K") || !strings.HasSuffix(got, "✗ Verification failed\n") {
		t.Fatalf("interactive output did not animate and settle cleanly: %q", got)
	}
}

func TestActivityCanOnlyFinishOnce(t *testing.T) {
	var out bytes.Buffer
	a := startActivityMode(&out, "Working", false)
	a.success("Done")
	a.failure("Too late")
	if strings.Contains(out.String(), "Too late") {
		t.Fatalf("activity rendered twice: %q", out.String())
	}
}
