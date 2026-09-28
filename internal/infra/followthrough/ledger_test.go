package followthrough

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

func TestMissingLedgerIsEmpty(t *testing.T) {
	got, err := Ledger{Path: filepath.Join(t.TempDir(), "none.jsonl")}.Load()
	if err != nil || len(got) != 0 {
		t.Fatalf("Load = %v, %v; want empty", got, err)
	}
}

func TestAppendAndLoad(t *testing.T) {
	l := Ledger{Path: filepath.Join(t.TempDir(), "coach", "followthrough.jsonl")}
	now := time.Now().UTC()
	if err := l.Append(ft.Entry{Type: ft.EntryOffer, ID: "a", At: now, Power: "models", Channel: ft.ChannelAdvice, Kind: "lookup"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ft.Entry{Type: ft.EntryResolve, ID: "a", At: now, Outcome: ft.OutcomeFollowed}); err != nil {
		t.Fatal(err)
	}
	got, err := l.Load()
	if err != nil || len(got) != 2 || got[1].Outcome != ft.OutcomeFollowed {
		t.Fatalf("Load = %+v, %v", got, err)
	}
	st, err := os.Stat(l.Path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("ledger mode = %v, %v; want 0600", st.Mode().Perm(), err)
	}
}

func TestATornLineDoesNotHideTheRest(t *testing.T) {
	l := Ledger{Path: filepath.Join(t.TempDir(), "f.jsonl")}
	body := `{"type":"offer","id":"a","power":"models"}` + "\n" + `{"type":"off` + "\n" + `{"type":"offer","id":"b","power":"models"}` + "\n"
	if err := os.WriteFile(l.Path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ := l.Load()
	if len(got) != 2 {
		t.Fatalf("Load = %+v; want the two whole lines", got)
	}
}

func TestConcurrentAppendsAllLand(t *testing.T) {
	l := Ledger{Path: filepath.Join(t.TempDir(), "f.jsonl")}
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Go(func() {
			_ = l.Append(ft.Entry{Type: ft.EntryOffer, ID: fmt.Sprint(i), At: time.Now(), Power: "models"})
		})
	}
	wg.Wait()
	got, _ := l.Load()
	if len(got) != 40 {
		t.Fatalf("entries = %d; want 40", len(got))
	}
}

func TestALargeLedgerIsPruned(t *testing.T) {
	l := Ledger{Path: filepath.Join(t.TempDir(), "f.jsonl")}
	oldAt, pad := time.Now().Add(-2*keep).Format(time.RFC3339), strings.Repeat("x", 500)
	var sb strings.Builder
	for i := 0; sb.Len() < pruneAbove; i++ {
		fmt.Fprintf(&sb, `{"type":"offer","id":"old-%d","at":%q,"power":"models","evidence":%q}`+"\n", i, oldAt, pad)
	}
	if err := os.WriteFile(l.Path, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ft.Entry{Type: ft.EntryOffer, ID: "new", At: time.Now(), Power: "models"}); err != nil {
		t.Fatal(err)
	}
	got, _ := l.Load()
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("after prune = %d entries; want only the new one", len(got))
	}
}
