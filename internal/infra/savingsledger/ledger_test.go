package savingsledger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/learning"
)

func TestMissingLedgerIsEmpty(t *testing.T) {
	got, err := Ledger{Path: filepath.Join(t.TempDir(), "none.jsonl")}.Load()
	if err != nil || len(got) != 0 {
		t.Fatalf("Load = %v, %v; want empty", got, err)
	}
}

func TestAppendAndLoadIsPrivate(t *testing.T) {
	l := Ledger{Path: filepath.Join(t.TempDir(), "coach", "savings.jsonl")}
	now := time.Now().UTC()
	if err := l.Append(learning.SavingsEntry{Type: learning.SavingsIntervention, ID: "a", At: now, Cohort: learning.CohortHoldout}); err != nil {
		t.Fatal(err)
	}
	got, err := l.Load()
	if err != nil || len(got) != 1 || got[0].Cohort != learning.CohortHoldout {
		t.Fatalf("Load = %+v, %v", got, err)
	}
	st, err := os.Stat(l.Path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v; want 0600", st.Mode().Perm(), err)
	}
}

func TestConcurrentAppendsAllLand(t *testing.T) {
	l := Ledger{Path: filepath.Join(t.TempDir(), "s.jsonl")}
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Go(func() {
			_ = l.Append(learning.SavingsEntry{Type: learning.SavingsIntervention, ID: fmt.Sprint(i), At: time.Now()})
		})
	}
	wg.Wait()
	if got, _ := l.Load(); len(got) != 40 {
		t.Fatalf("entries = %d; want 40", len(got))
	}
}

func TestALargeLedgerIsPruned(t *testing.T) {
	l := Ledger{Path: filepath.Join(t.TempDir(), "s.jsonl")}
	oldAt, pad := time.Now().Add(-2*keep).Format(time.RFC3339), strings.Repeat("x", 500)
	var sb strings.Builder
	for i := 0; sb.Len() < pruneAbove; i++ {
		fmt.Fprintf(&sb, `{"type":"intervention","id":"old-%d","at":%q,"evidence":%q}`+"\n", i, oldAt, pad)
	}
	if err := os.WriteFile(l.Path, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(learning.SavingsEntry{Type: learning.SavingsIntervention, ID: "new", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if got, _ := l.Load(); len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("after prune = %d entries; want only the new one", len(got))
	}
}
