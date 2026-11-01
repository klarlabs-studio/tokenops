package cli

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/fmtinsight"
	"go.klarlabs.de/tokenops/internal/config"
)

// compressible is output the generic scrub shortens: a repeated line and
// a run of blanks.
const compressible = `printf 'a\na\n\n\n\nb\n'`

// In the raw arm the agent gets stdout byte for byte, and the run still
// records what fmt would have saved.
func TestRunFmtRawArmPassesOutputThrough(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	argv := []string{"sh", "-c", compressible}
	res, err := runFmt(context.Background(), testRegistry(), argv, fmtOptions{RecoverDir: dir, Raw: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := "a\na\n\n\n\nb\n"; string(res.Stdout) != want {
		t.Fatalf("raw arm stdout = %q, want %q", res.Stdout, want)
	}
	if !res.Raw || res.Compressed || res.BytesAfter != res.BytesBefore {
		t.Errorf("raw arm result: %+v", res)
	}
	if res.CompactBytes >= res.BytesBefore {
		t.Errorf("no would-be saving recorded: compact %d of %d", res.CompactBytes, res.BytesBefore)
	}

	exp := fmtExperimentRun{Session: "s1", Arm: fmtinsight.ArmRaw, RawPercent: 50}
	if err := recordCompressRun(dir, "sh", "balanced", res, exp, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	recs, err := readLearnRecords(dir)
	if err != nil || len(recs) != 1 {
		t.Fatalf("records: %v, %v", recs, err)
	}
	r := recs[0]
	if r.Session != "s1" || r.Arm != fmtinsight.ArmRaw || r.RawPercent != 50 || r.CompactBytes != int64(res.CompactBytes) {
		t.Errorf("record: %+v", r)
	}
	if r.TokensSaved != int64(estTokens(res.BytesBefore-res.CompactBytes)) {
		t.Errorf("would-have-saved = %d", r.TokensSaved)
	}
}

// The raw arm forwards a failing command's stdout as it does any other,
// without a would-be saving: fmt would have forwarded it raw too.
func TestRunFmtRawArmOnError(t *testing.T) {
	skipOnWindows(t)
	argv := []string{"sh", "-c", compressible + "; exit 2"}
	res, err := runFmt(context.Background(), testRegistry(), argv, fmtOptions{NoRecover: true, RawOnError: true, Raw: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Raw || res.ExitCode != 2 || res.CompactBytes != res.BytesBefore {
		t.Errorf("raw arm on error: %+v", res)
	}
}

// The fmt arm is fmt as it always was: the experiment changes nothing in
// what the engine produces for it.
func TestRunFmtFmtArmIsUnchanged(t *testing.T) {
	skipOnWindows(t)
	argv := []string{"sh", "-c", compressible}
	plain, err := runFmt(context.Background(), testRegistry(), argv, fmtOptions{NoRecover: true})
	if err != nil {
		t.Fatal(err)
	}
	session := "s0"
	for i := 1; fmtinsight.ExperimentArm(session, 50) != fmtinsight.ArmFmt; i++ {
		session = "s" + strconv.Itoa(i)
	}
	exp := fmtExperiment(50, func(string) string { return session })
	inArm, err := runFmt(context.Background(), testRegistry(), argv, fmtOptions{NoRecover: true, Raw: exp.Arm == fmtinsight.ArmRaw})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain.Stdout, inArm.Stdout) || inArm.Raw || inArm.BytesAfter != plain.BytesAfter || inArm.BytesAfter >= inArm.BytesBefore {
		t.Errorf("fmt arm output %q differs from fmt's %q", inArm.Stdout, plain.Stdout)
	}
}

func TestFmtExperimentReadsTheSession(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	claude := env(map[string]string{"CLAUDE_CODE_SESSION_ID": "cc-1"})
	if got := fmtExperiment(0, claude); got != (fmtExperimentRun{}) {
		t.Errorf("off: %+v", got)
	}
	got := fmtExperiment(50, claude)
	if got.Session != "cc-1" || got.RawPercent != 50 || got.Arm != fmtinsight.ExperimentArm("cc-1", 50) {
		t.Errorf("claude code session: %+v", got)
	}
	both := env(map[string]string{"TOKENOPS_SESSION_ID": "explicit", "CLAUDE_CODE_SESSION_ID": "cc-1"})
	if got := fmtExperiment(50, both); got.Session != "explicit" {
		t.Errorf("explicit session not preferred: %+v", got)
	}
	if got := fmtExperiment(50, env(nil)); got != (fmtExperimentRun{}) {
		t.Errorf("no session: %+v", got)
	}
}

// The CLI is the operator: `tokenops fmt experiment on` writes the share,
// and with no argument it reports the setting and the comparison.
func TestFmtExperimentCommand(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, home)
	}
	t.Setenv("TOKENOPS_STORAGE_PATH", home+"/no-events.db")
	writeCoachConfig(t, "")
	recover := t.TempDir()

	out := runRoot(t, "fmt", "experiment", "on", "--recover-dir", recover)
	if !strings.Contains(out, "on, 50% of sessions") || !strings.Contains(out, "not enough sessions yet") {
		t.Errorf("on:\n%s", out)
	}
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ReadMutable(path)
	if err != nil || cfg.Optimizer.CommandFmt.Experiment != 50 {
		t.Fatalf("config after on: %d, %v", cfg.Optimizer.CommandFmt.Experiment, err)
	}
	if _, err := runCLI(t, nil, "fmt", "experiment", "80"); err == nil {
		t.Error("80% accepted")
	}
	if out := runRoot(t, "fmt", "experiment", "off", "--recover-dir", recover); !strings.Contains(out, "fmt experiment: off") {
		t.Errorf("off:\n%s", out)
	}
}
