package accounts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeCLI writes an executable that answers like a vendor CLI: for each
// first argument in answers, it prints the file or text given and exits
// with the status given. It stands in for the real CLI, which no test
// runs. args.log records every invocation's arguments.
func fakeCLI(t *testing.T, answers map[string]fakeAnswer) (bin, log string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a shell script")
	}
	dir := t.TempDir()
	log = filepath.Join(dir, "args.log")
	var b strings.Builder
	b.WriteString("#!/bin/sh\necho \"$@\" >> '" + log + "'\ncase \"$1\" in\n")
	for arg, a := range answers {
		out := filepath.Join(dir, arg+".out")
		if err := os.WriteFile(out, []byte(a.output), 0o600); err != nil {
			t.Fatal(err)
		}
		b.WriteString("  '" + arg + "') cat '" + out + "'; exit " + a.status + " ;;\n")
	}
	b.WriteString("esac\nexit 2\n")
	bin = filepath.Join(dir, "vendor-cli")
	if err := os.WriteFile(bin, []byte(b.String()), 0o700); err != nil { //nolint:gosec // a test executable
		t.Fatal(err)
	}
	return bin, log
}

type fakeAnswer struct {
	output string
	status string
}

func ok(output string) fakeAnswer     { return fakeAnswer{output: output, status: "0"} }
func failed(output string) fakeAnswer { return fakeAnswer{output: output, status: "1"} }

func invocations(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestRunCLIBoundsAndStrips(t *testing.T) {
	bin, _ := fakeCLI(t, map[string]fakeAnswer{"usage": ok("\x1b[1mbold\x1b[0m\r\n"), "fail": failed("nope")})
	out, err := runCLI(context.Background(), 0, bin, nil, "usage")
	if err != nil || out != "bold\n" {
		t.Errorf("out %q, %v", out, err)
	}
	out, err = runCLI(context.Background(), 0, bin, nil, "fail")
	if !errors.Is(err, errCLIFailed) || out != "nope" || strings.Contains(err.Error(), "nope") {
		t.Errorf("failure: %q, %v", out, err)
	}
}

func TestRunCLITimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	bin := filepath.Join(t.TempDir(), "slow")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil { //nolint:gosec // a test executable
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := runCLI(context.Background(), 200*time.Millisecond, bin, nil); err == nil || time.Since(start) > 4*time.Second {
		t.Errorf("err %v after %s", err, time.Since(start))
	}
}

func TestLocateCLIOverride(t *testing.T) {
	if _, found := locateCLI("no-such-vendor-cli", filepath.Join(t.TempDir(), "missing")); found {
		t.Error("a missing override was found")
	}
	bin, _ := fakeCLI(t, nil)
	if got, found := locateCLI("no-such-vendor-cli", bin); !found || got != bin {
		t.Errorf("override %q %v", got, found)
	}
}
