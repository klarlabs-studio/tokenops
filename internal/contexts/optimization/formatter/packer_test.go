package formatter

import (
	"strings"
	"testing"
)

// packerFailRaw is `packer build` output for a build that errors during
// provisioning: per-step progress, indented output echoes, and a terminal
// errored/didn't-complete result.
const packerFailRaw = `==> amazon-ebs: Prevalidating any provided VPC information
==> amazon-ebs: Creating temporary keypair: packer_abc123
    amazon-ebs: Found image ID: ami-0abcd1234
==> amazon-ebs: Launching a source AWS instance...
    amazon-ebs: Instance ID: i-0abcd1234
==> amazon-ebs: Provisioning with shell script: /tmp/script.sh
    amazon-ebs: installing dependencies...
    amazon-ebs: error: package not found
==> amazon-ebs: Terminating the source AWS instance...
Build 'amazon-ebs' errored after 2 minutes 3 seconds: Script exited with non-zero exit status: 1
==> Some builds didn't complete successfully and had errors:
--> amazon-ebs: Script exited with non-zero exit status: 1
`

// packerOkRaw is `packer build` output for a successful build.
const packerOkRaw = `==> amazon-ebs: Prevalidating any provided VPC information
==> amazon-ebs: Creating temporary keypair: packer_xyz789
    amazon-ebs: output line one
==> amazon-ebs: Launching a source AWS instance...
    amazon-ebs: Instance ID: i-0abcd5678
==> amazon-ebs: Provisioning with shell script: /tmp/setup.sh
    amazon-ebs: installing packages...
==> amazon-ebs: Stopping the source instance...
==> amazon-ebs: Creating AMI ami-0newimage from instance
Build 'amazon-ebs' finished after 5 minutes 12 seconds.
==> Builds finished. The artifacts of successful builds are:
--> amazon-ebs: AMIs were created: us-east-1: ami-0newimage
`

func TestPacker_CriticalSurvivesEveryLevel(t *testing.T) {
	p := NewPacker()
	critical := []string{
		"error: package not found",
		"Build 'amazon-ebs' errored after 2 minutes",
		"Some builds didn't complete successfully",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := p.Format([]byte(packerFailRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Fatalf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		for _, c := range critical {
			if !strings.Contains(compact, c) {
				t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, c, compact)
			}
		}
	}
}

func TestPacker_BalancedDropsNoise(t *testing.T) {
	p := NewPacker()
	res, _ := p.Format([]byte(packerFailRaw), LossBalanced)
	compact := string(res.Compact)

	// Per-step progress and indented output echoes are noise at Balanced+.
	for _, noise := range []string{
		"Prevalidating any provided VPC",
		"Launching a source AWS instance",
		"installing dependencies",
		"Found image ID",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The build-result and error lines must survive.
	for _, keep := range []string{
		"error: package not found",
		"errored after",
		"didn't complete",
	} {
		if !strings.Contains(compact, keep) {
			t.Errorf("balanced dropped %q:\n%s", keep, compact)
		}
	}
	if !res.CriticalKept {
		t.Error("balanced CriticalKept=false")
	}
}

func TestPacker_MonotonicReduction(t *testing.T) {
	p := NewPacker()
	for _, raw := range []string{packerFailRaw, packerOkRaw} {
		last := 1 << 30
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, _ := p.Format([]byte(raw), level)
			if res.BytesAfter > last {
				t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
			}
			last = res.BytesAfter
		}
	}
}

func TestPacker_NonPackerFallsBackToGeneric(t *testing.T) {
	p := NewPacker()
	raw := "some unrelated log line\nanother line without build signal\n"
	res, ok := p.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
