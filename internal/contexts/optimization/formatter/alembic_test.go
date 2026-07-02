package formatter

import (
	"strings"
	"testing"
)

const alembicMigrateRaw = `INFO  [alembic.runtime.migration] Context impl PostgresqlImpl.
INFO  [alembic.runtime.migration] Will assume transactional DDL.
INFO  [alembic.runtime.migration] Running upgrade abc123 -> def456, add users table
`

const alembicErrorRaw = `INFO  [alembic.runtime.migration] Context impl PostgresqlImpl.
INFO  [alembic.runtime.migration] Will assume transactional DDL.
INFO  [alembic.runtime.migration] Running upgrade abc123 -> def456, add users table
Traceback (most recent call last):
  File "alembic/runtime/migration.py", line 123, in run_migrations
sqlalchemy.exc.ProgrammingError: relation "users" already exists
FAILED: relation "users" already exists
`

func TestAlembic_CriticalSurvivesEveryLevel(t *testing.T) {
	a := NewAlembic()
	cases := map[string][]string{
		alembicMigrateRaw: {
			"Running upgrade abc123 -> def456, add users table",
		},
		alembicErrorRaw: {
			"Running upgrade abc123 -> def456, add users table",
			"Traceback (most recent call last):",
			`sqlalchemy.exc.ProgrammingError: relation "users" already exists`,
			`FAILED: relation "users" already exists`,
		},
	}
	for raw, critical := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := a.Format([]byte(raw), level)
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
}

func TestAlembic_BalancedDropsBoilerplate(t *testing.T) {
	a := NewAlembic()
	res, _ := a.Format([]byte(alembicMigrateRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"Context impl", "Will assume transactional DDL"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept boilerplate %q:\n%s", noise, compact)
		}
	}
	// The migration line must survive.
	if !strings.Contains(compact, "Running upgrade") {
		t.Errorf("balanced dropped the migration line:\n%s", compact)
	}
}

func TestAlembic_MonotonicReduction(t *testing.T) {
	a := NewAlembic()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := a.Format([]byte(alembicMigrateRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestAlembic_NonMatchingFallsBackToGeneric(t *testing.T) {
	a := NewAlembic()
	raw := "some unrelated tool output line one\nsome unrelated tool output line two\n"
	res, ok := a.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
