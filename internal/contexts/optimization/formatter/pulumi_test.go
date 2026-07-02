package formatter

import (
	"strings"
	"testing"
)

const pulumiPreviewRaw = `Previewing update (dev):

@ Previewing update.......

     Type                          Name         Plan       Info
    + aws:s3/bucket:Bucket         my-bucket    create
        bucket: "my-bucket"
        acl: "private"
    ~ aws:ec2/instance:Instance    web          update
        [id=i-1234567890abcdef0]
        ami: "ami-0c55b159cbfafe1f0"
      ~ instanceType: "t2.micro" => "t2.small"

@ Previewing update....

Resources:
    + 2 to create
    ~ 1 to update

Outputs:
    bucketName: "my-bucket"
`

const pulumiErrorRaw = `Previewing update (dev):

@ Previewing update....

     Type                     Name         Plan       Info
    + aws:s3/bucket:Bucket    my-bucket    create     1 error

Diagnostics:
  aws:s3/bucket:Bucket (my-bucket):
    error: failed to create resource

error: preview failed
`

func TestPulumi_CriticalSurvivesEveryLevel(t *testing.T) {
	p := NewPulumi()
	previewCritical := []string{
		"+ aws:s3/bucket:Bucket         my-bucket    create",
		"~ aws:ec2/instance:Instance    web          update",
		"Resources:",
		"+ 2 to create",
		"~ 1 to update",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := p.Format([]byte(pulumiPreviewRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Fatalf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		for _, c := range previewCritical {
			if !strings.Contains(compact, strings.TrimSpace(c)) {
				t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, c, compact)
			}
		}

		eres, ok := p.Format([]byte(pulumiErrorRaw), level)
		if !ok {
			t.Fatalf("error level=%s ok=false", level)
		}
		if !eres.CriticalKept {
			t.Fatalf("error level=%s CriticalKept=false", level)
		}
		if !strings.Contains(string(eres.Compact), "error: failed to create resource") {
			t.Errorf("level=%s dropped error line\ngot:\n%s", level, string(eres.Compact))
		}
	}
}

func TestPulumi_BalancedDropsUnchanged(t *testing.T) {
	p := NewPulumi()
	res, _ := p.Format([]byte(pulumiPreviewRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		`acl: "private"`,               // unchanged property
		`ami: "ami-0c55b159cbfafe1f0"`, // unchanged property
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept unchanged property %q:\n%s", noise, compact)
		}
	}
	// The changed property and resource operations must survive.
	if !strings.Contains(compact, "instanceType") {
		t.Errorf("balanced dropped the changed property:\n%s", compact)
	}
	if !strings.Contains(compact, "aws:s3/bucket:Bucket") {
		t.Errorf("balanced dropped the resource operation:\n%s", compact)
	}
	if !strings.Contains(compact, "Resources:") {
		t.Errorf("balanced dropped the summary:\n%s", compact)
	}
}

func TestPulumi_MonotonicReduction(t *testing.T) {
	p := NewPulumi()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := p.Format([]byte(pulumiPreviewRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestPulumi_NonPulumiFallsBackToGeneric(t *testing.T) {
	p := NewPulumi()
	raw := "hello world\nthis is not infra output\nsome unrelated log\n"
	res, ok := p.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
