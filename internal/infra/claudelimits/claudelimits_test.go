package claudelimits

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", fileName)
	used, limit := 314.12, 500.0
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	in := Reading{
		ObservedAt: at,
		FiveHour:   &Window{UsedPct: 23.5, ResetsAt: 1738425600},
		SpendLimit: &Window{UsedPct: 62.8, ResetsAt: 1740787200, UsedUSD: &used, LimitUSD: &limit, Period: "monthly"},
	}
	if err := Write(path, in); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Read(path)
	if err != nil || !ok {
		t.Fatalf("Read = ok %v, err %v", ok, err)
	}
	if !got.ObservedAt.Equal(at) || got.FiveHour.UsedPct != 23.5 || got.SevenDay != nil {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if *got.SpendLimit.UsedUSD != used || *got.SpendLimit.LimitUSD != limit || got.SpendLimit.Period != "monthly" {
		t.Fatalf("spend limit lost: %+v", got.SpendLimit)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestEmptyReadingIsNotWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), fileName)
	if err := Write(path, Reading{ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an empty reading was written: %v", err)
	}
	if _, ok, err := Read(path); ok || err != nil {
		t.Fatalf("Read of a missing file = ok %v, err %v", ok, err)
	}
}
