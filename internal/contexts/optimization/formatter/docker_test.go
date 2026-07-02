package formatter

import (
	"strings"
	"testing"
)

// dockerBuildRaw is a realistic classic `docker build` transcript: a base
// image pull with fs-layer / download / extract progress, several
// "Step N/M" instructions interleaved with "---> Using cache" and
// "---> Running in …" intermediate-container lines, and the final
// "Successfully built" / "Successfully tagged" result.
const dockerBuildRaw = `Step 1/8 : FROM node:18
18: Pulling from library/node
a1b2c3d4e5f6: Pulling fs layer
b2c3d4e5f6a7: Pulling fs layer
a1b2c3d4e5f6: Downloading [====>              ]  2.5MB/10MB
a1b2c3d4e5f6: Verifying Checksum
a1b2c3d4e5f6: Download complete
a1b2c3d4e5f6: Extracting [=========>         ]  8MB/10MB
a1b2c3d4e5f6: Pull complete
Digest: sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789
Status: Downloaded newer image for node:18
 ---> 1234567890ab
Step 2/8 : WORKDIR /app
 ---> Running in abc123def456
 ---> 234567890abc
Step 3/8 : COPY package.json ./
 ---> 34567890abcd
Step 4/8 : RUN npm ci
 ---> Running in def456abc789
 ---> Using cache
 ---> 4567890abcde
Step 5/8 : COPY . .
 ---> 567890abcdef
Step 6/8 : RUN npm run build
 ---> Running in 789abc123def
 ---> 67890abcdef0
Step 7/8 : EXPOSE 3000
 ---> Running in 890abcdef012
 ---> 7890abcdef01
Step 8/8 : CMD ["node", "server.js"]
 ---> Running in 90abcdef0123
 ---> 890abcdef012
Successfully built 890abcdef012
Successfully tagged myapp:latest
`

// dockerBuildkitFailRaw is a buildkit `docker build` that fails on a RUN
// step: the "=> ERROR" step line plus the terminal "failed to solve" line.
const dockerBuildkitFailRaw = `#1 [internal] load build definition from Dockerfile
#1 transferring dockerfile: 234B done
#1 DONE 0.0s
#5 [build 1/5] FROM docker.io/library/node:18
#5 CACHED
=> [build 2/5] COPY package.json ./
=> => transferring context: 1.2kB done
=> ERROR [build 3/5] RUN npm ci
------
 > [build 3/5] RUN npm ci:
0.521 npm error code ERESOLVE
------
failed to solve: process "/bin/sh -c npm ci" did not complete successfully: exit code: 1
`

func TestDocker_CriticalSurvivesEveryLevel(t *testing.T) {
	d := NewDocker()
	cases := []struct {
		raw      string
		critical []string
	}{
		{
			raw: dockerBuildRaw,
			critical: []string{
				"Successfully built 890abcdef012",
				"Successfully tagged myapp:latest",
			},
		},
		{
			raw: dockerBuildkitFailRaw,
			critical: []string{
				"=> ERROR [build 3/5] RUN npm ci",
				`failed to solve: process "/bin/sh -c npm ci" did not complete successfully: exit code: 1`,
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

func TestDocker_BalancedDropsLayerNoise(t *testing.T) {
	d := NewDocker()
	res, _ := d.Format([]byte(dockerBuildRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"Pulling fs layer",
		"Extracting",
		"---> Using cache",
		"---> Running in",
		"Download complete",
		"Verifying Checksum",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept layer noise %q:\n%s", noise, compact)
		}
	}
	// The Step lines survive at Balanced.
	if !strings.Contains(compact, "Step 5/8") {
		t.Errorf("balanced should keep Step lines:\n%s", compact)
	}
}

func TestDocker_AggressiveCollapsesSteps(t *testing.T) {
	d := NewDocker()
	res, _ := d.Format([]byte(dockerBuildRaw), LossAggressive)
	compact := string(res.Compact)
	// The first step stays; middle steps collapse into a count.
	if !strings.Contains(compact, "Step 1/8") {
		t.Errorf("aggressive should keep the first Step line:\n%s", compact)
	}
	for _, step := range []string{"Step 5/8", "Step 8/8"} {
		if strings.Contains(compact, step) {
			t.Errorf("aggressive should collapse middle Step %q:\n%s", step, compact)
		}
	}
	if !strings.Contains(compact, "build steps") {
		t.Errorf("aggressive should emit a build-step count:\n%s", compact)
	}
	// The criticals must still be present.
	for _, c := range []string{"Successfully built 890abcdef012", "Successfully tagged myapp:latest"} {
		if !strings.Contains(compact, c) {
			t.Errorf("aggressive dropped critical %q:\n%s", c, compact)
		}
	}
}

func TestDocker_MonotonicReduction(t *testing.T) {
	d := NewDocker()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := d.Format([]byte(dockerBuildRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestDocker_NonDockerFallsBackToGeneric(t *testing.T) {
	d := NewDocker()
	raw := "Cloning into 'repo'...\nremote: Counting objects: 100% (42/42), done.\nUnpacking objects: 100% (42/42), done.\n"
	res, ok := d.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestDocker_CriticalLineClassification(t *testing.T) {
	d := NewDocker()
	for _, line := range []string{
		"ERROR: failed to build",
		"ERROR [build 3/5] RUN npm ci",
		`failed to solve: process "/bin/sh -c npm ci" did not complete successfully`,
		"=> ERROR [build 3/5] RUN npm ci",
		"Successfully built 890abcdef012",
		"Successfully tagged myapp:latest",
		"naming to docker.io/library/myapp:latest done",
		"gcc: error: no input files",
	} {
		if !d.CriticalLine(line) {
			t.Errorf("expected critical: %q", line)
		}
	}
	for _, line := range []string{
		"Step 3/8 : COPY package.json ./",
		" ---> Using cache",
		" ---> Running in abc123def456",
		"a1b2c3d4e5f6: Pulling fs layer",
		"=> => transferring context: 1.2kB done",
	} {
		if d.CriticalLine(line) {
			t.Errorf("expected non-critical: %q", line)
		}
	}
}
