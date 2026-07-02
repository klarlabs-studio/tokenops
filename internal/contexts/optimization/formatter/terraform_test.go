package formatter

import (
	"strings"
	"testing"
)

const terraformPlanRaw = `aws_instance.web: Refreshing state... [id=i-1234567890abcdef0]
data.aws_ami.ubuntu: Refreshing state... [id=ami-0c55b159cbfafe1f0]

Terraform used the selected providers to generate the following execution
plan. Resource actions are indicated with the following symbols:
  ~ update in-place

Terraform will perform the following actions:

  # aws_instance.web will be updated in-place
  ~ resource "aws_instance" "web" {
        ami                          = "ami-0c55b159cbfafe1f0"
        id                           = "i-1234567890abcdef0"
      ~ instance_type                = "t2.micro" -> "t2.small"
        tags                         = {
            "Name" = "web-server"
        }
        arn                          = (known after apply)
    }

Plan: 0 to add, 1 to change, 0 to destroy.
`

const terraformErrorRaw = `aws_instance.web: Refreshing state... [id=i-1234567890abcdef0]

Error: Invalid resource type

  on main.tf line 1, in resource "aws_instances" "web":
   1: resource "aws_instances" "web" {

The provider hashicorp/aws does not support resource type "aws_instances".
`

func TestTerraform_CriticalSurvivesEveryLevel(t *testing.T) {
	tf := NewTerraform()
	planCritical := []string{
		"# aws_instance.web will be updated in-place",
		"~ instance_type                = \"t2.micro\" -> \"t2.small\"",
		"Plan: 0 to add, 1 to change, 0 to destroy.",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := tf.Format([]byte(terraformPlanRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Fatalf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		for _, c := range planCritical {
			if !strings.Contains(compact, strings.TrimSpace(c)) {
				t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, c, compact)
			}
		}

		eres, ok := tf.Format([]byte(terraformErrorRaw), level)
		if !ok {
			t.Fatalf("error level=%s ok=false", level)
		}
		if !eres.CriticalKept {
			t.Fatalf("error level=%s CriticalKept=false", level)
		}
		if !strings.Contains(string(eres.Compact), "Error: Invalid resource type") {
			t.Errorf("level=%s dropped Error line\ngot:\n%s", level, string(eres.Compact))
		}
	}
}

func TestTerraform_BalancedDropsRefreshing(t *testing.T) {
	tf := NewTerraform()
	res, _ := tf.Format([]byte(terraformPlanRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"Refreshing state...", // refresh chatter
		"web-server",          // unchanged tags attribute value
		"(known after apply)", // filler
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The changed attribute and header must survive.
	if !strings.Contains(compact, "instance_type") {
		t.Errorf("balanced dropped the changed attribute:\n%s", compact)
	}
	if !strings.Contains(compact, "# aws_instance.web") {
		t.Errorf("balanced dropped the resource header:\n%s", compact)
	}
}

func TestTerraform_AggressiveKeepsOnlyChanges(t *testing.T) {
	tf := NewTerraform()
	res, _ := tf.Format([]byte(terraformPlanRaw), LossAggressive)
	compact := string(res.Compact)
	// Header, changed attribute, and Plan summary stay.
	for _, keep := range []string{"# aws_instance.web", "instance_type", "Plan: 0 to add"} {
		if !strings.Contains(compact, keep) {
			t.Errorf("aggressive dropped %q:\n%s", keep, compact)
		}
	}
	// Surrounding context is gone.
	for _, drop := range []string{
		"selected providers",  // preamble
		"following actions",   // preamble
		"Refreshing state...", // refresh chatter
		"web-server",          // unchanged attribute
	} {
		if strings.Contains(compact, drop) {
			t.Errorf("aggressive kept context %q:\n%s", drop, compact)
		}
	}
}

func TestTerraform_MonotonicReduction(t *testing.T) {
	tf := NewTerraform()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := tf.Format([]byte(terraformPlanRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestTerraform_NonTerraformFallsBackToGeneric(t *testing.T) {
	tf := NewTerraform()
	raw := "hello world\nthis is not infra output\nsome unrelated log\n"
	res, ok := tf.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
