package formatter

import (
	"strings"
	"testing"
)

const flywayMigrateRaw = `Flyway Community Edition 9.22.0 by Redgate
Database: jdbc:postgresql://localhost:5432/app (PostgreSQL 15.3)
Successfully validated 12 migrations (execution time 00:00.031s)
Current version of schema "public": 11
Migrating schema "public" to version 12 - add users table
Successfully applied 1 migration to schema "public" (execution time 00:00.042s)
`

const flywayErrorRaw = `Flyway Community Edition 9.22.0 by Redgate
Database: jdbc:postgresql://localhost:5432/app (PostgreSQL 15.3)
Successfully validated 12 migrations (execution time 00:00.031s)
Current version of schema "public": 11
Migrating schema "public" to version 12 - add users table
ERROR: Migration V12__add_users_table.sql failed
SQL State  : 42P07
Error Code : 0
Message    : ERROR: relation "users" already exists
`

func TestFlyway_CriticalSurvivesEveryLevel(t *testing.T) {
	f := NewFlyway()
	cases := map[string][]string{
		flywayMigrateRaw: {
			`Migrating schema "public" to version 12 - add users table`,
			`Successfully applied 1 migration to schema "public" (execution time 00:00.042s)`,
		},
		flywayErrorRaw: {
			`Migrating schema "public" to version 12 - add users table`,
			`ERROR: Migration V12__add_users_table.sql failed`,
		},
	}
	for raw, critical := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := f.Format([]byte(raw), level)
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

func TestFlyway_BalancedDropsBoilerplate(t *testing.T) {
	f := NewFlyway()
	res, _ := f.Format([]byte(flywayMigrateRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"by Redgate", "Database:", "Successfully validated", "Current version"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept boilerplate %q:\n%s", noise, compact)
		}
	}
	// The state-bearing lines must survive.
	if !strings.Contains(compact, "Migrating schema") {
		t.Errorf("balanced dropped the migrate line:\n%s", compact)
	}
	if !strings.Contains(compact, "Successfully applied") {
		t.Errorf("balanced dropped the applied summary:\n%s", compact)
	}
}

func TestFlyway_MonotonicReduction(t *testing.T) {
	f := NewFlyway()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := f.Format([]byte(flywayMigrateRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestFlyway_NonMatchingFallsBackToGeneric(t *testing.T) {
	f := NewFlyway()
	raw := "some unrelated tool output line one\nsome unrelated tool output line two\n"
	res, ok := f.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
