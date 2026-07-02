package formatter

import (
	"strings"
	"testing"
)

// mvnFailureRaw is a realistic `mvn package` transcript that fails to
// compile: the noisy "[INFO]" lifecycle banner, a couple "[WARNING]" lines,
// an artifact download line, the surefire "Tests run:" summary with a
// failure, several "[ERROR]" compiler lines, and the terminal
// "BUILD FAILURE" result.
const mvnFailureRaw = `[INFO] Scanning for projects...
[INFO]
[INFO] ------------------------< com.example:my-app >------------------------
[INFO] Building my-app 1.0
[INFO] --------------------------------[ jar ]---------------------------------
Downloading from central: https://repo.maven.apache.org/maven2/junit/junit/4.13.2/junit-4.13.2.pom
[INFO]
[INFO] --- resources:3.3.0:resources (default-resources) ---
[INFO] Using 'UTF-8' encoding to copy filtered resources.
[INFO] Copying 1 resource
[INFO]
[INFO] --- compiler:compile ---
[INFO] Changes detected - recompiling the module!
[WARNING] File encoding has not been set, using platform encoding UTF-8
[WARNING] system modules path not set in conjunction with -source 8
[INFO] Compiling 3 source files to /home/user/my-app/target/classes
[INFO]
[INFO] --- surefire:3.2.5:test (default-test) ---
[INFO] Tests run: 12, Failures: 1, Errors: 0, Skipped: 0
[INFO]
[INFO] ------------------------------------------------------------------------
[INFO] BUILD FAILURE
[INFO] ------------------------------------------------------------------------
[ERROR] /src/Main.java:[10,5] cannot find symbol
[ERROR]   symbol:   variable foo
[ERROR]   location: class com.example.Main
[ERROR] /src/Util.java:[22,9] cannot find symbol
[ERROR] Failed to execute goal on project my-app: Compilation failure
`

// mvnSuccessRaw is a clean build that succeeds.
const mvnSuccessRaw = `[INFO] Scanning for projects...
[INFO] Building my-app 1.0
[INFO] --- compiler:compile ---
[INFO] Compiling 3 source files to /home/user/my-app/target/classes
Downloading from central: https://repo.maven.apache.org/maven2/junit/junit/4.13.2/junit-4.13.2.jar
[INFO] Tests run: 8, Failures: 0, Errors: 0, Skipped: 0
[INFO] BUILD SUCCESS
[INFO] Total time:  4.201 s
`

func TestMvn_CriticalSurvivesEveryLevel(t *testing.T) {
	m := NewMvn()
	cases := []struct {
		raw      string
		critical []string
	}{
		{
			raw: mvnFailureRaw,
			critical: []string{
				"[ERROR] /src/Main.java:[10,5] cannot find symbol",
				"[ERROR] Failed to execute goal on project my-app: Compilation failure",
				"[INFO] BUILD FAILURE",
				"[INFO] Tests run: 12, Failures: 1, Errors: 0, Skipped: 0",
			},
		},
		{
			raw: mvnSuccessRaw,
			critical: []string{
				"[INFO] BUILD SUCCESS",
				"[INFO] Tests run: 8, Failures: 0, Errors: 0, Skipped: 0",
			},
		},
	}
	for _, tc := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := m.Format([]byte(tc.raw), level)
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

func TestMvn_BalancedDropsInfoNoise(t *testing.T) {
	m := NewMvn()
	res, _ := m.Format([]byte(mvnFailureRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"[INFO] Scanning for projects",
		"[INFO] Building my-app 1.0",
		"[INFO] --- compiler:compile ---",
		"Downloading from central",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The result and test-summary signal must still be present.
	if !strings.Contains(compact, "BUILD FAILURE") {
		t.Errorf("balanced dropped BUILD FAILURE:\n%s", compact)
	}
	if !strings.Contains(compact, "Tests run: 12") {
		t.Errorf("balanced dropped the Tests run summary:\n%s", compact)
	}
}

func TestMvn_AggressiveDropsWarnings(t *testing.T) {
	m := NewMvn()
	balanced, _ := m.Format([]byte(mvnFailureRaw), LossBalanced)
	if !strings.Contains(string(balanced.Compact), "[WARNING]") {
		t.Fatalf("balanced should keep [WARNING] lines:\n%s", balanced.Compact)
	}
	res, _ := m.Format([]byte(mvnFailureRaw), LossAggressive)
	compact := string(res.Compact)
	if strings.Contains(compact, "[WARNING]") {
		t.Errorf("aggressive should drop [WARNING] lines:\n%s", compact)
	}
	// Criticals still survive.
	if !strings.Contains(compact, "BUILD FAILURE") {
		t.Errorf("aggressive dropped BUILD FAILURE:\n%s", compact)
	}
	if !strings.Contains(compact, "[ERROR] /src/Main.java") {
		t.Errorf("aggressive dropped an [ERROR] line:\n%s", compact)
	}
}

func TestMvn_MonotonicReduction(t *testing.T) {
	m := NewMvn()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := m.Format([]byte(mvnFailureRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestMvn_NonMvnFallsBackToGeneric(t *testing.T) {
	m := NewMvn()
	raw := "Cloning into 'repo'...\nremote: Counting objects: 100% (42/42), done.\nUnpacking objects: 100% (42/42), done.\n"
	res, ok := m.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestMvn_CriticalLineClassification(t *testing.T) {
	m := NewMvn()
	for _, line := range []string{
		"[ERROR] /src/Main.java:[10,5] cannot find symbol",
		"[INFO] BUILD FAILURE",
		"[INFO] BUILD SUCCESS",
		"[INFO] Tests run: 12, Failures: 1, Errors: 0, Skipped: 0",
	} {
		if !m.CriticalLine(line) {
			t.Errorf("expected critical: %q", line)
		}
	}
	for _, line := range []string{
		"[WARNING] File encoding has not been set, using platform encoding UTF-8",
		"[INFO] Building my-app 1.0",
		"Downloading from central: https://repo.maven.apache.org/maven2/junit/junit/4.13.2/junit-4.13.2.pom",
	} {
		if m.CriticalLine(line) {
			t.Errorf("expected non-critical: %q", line)
		}
	}
}
