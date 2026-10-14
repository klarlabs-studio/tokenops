package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/infra/followthrough"
)

// An unusual-spend finding is answered like a tip, and the answer says
// what it does to anomalies rather than to the session budget.
func TestCoachTipAnswersAnAnomaly(t *testing.T) {
	l, err := followthrough.Default()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(l.Path) })
	_ = os.Remove(l.Path)
	_ = l.Append(ft.Entry{Type: ft.EntryOffer, ID: "anomaly-spend-2026-10-14", At: time.Now(), Power: "inform",
		Channel: ft.ChannelTip, Kind: "anomaly_pricier_model", Evidence: "pricier_model:opus", Via: ft.ViaAmbient})

	out, err := runCoachCmd(t, "tip", "expected", "--id", "anomaly-spend-2026-10-14")
	if err != nil || !strings.Contains(out, "Recorded the unusual spend (a pricier model) as expected: the same pattern is no longer flagged.") {
		t.Fatalf("expected: %v %q", err, out)
	}
	out, err = runCoachCmd(t, "tip", "ack", "--id", "anomaly-spend-2026-10-14")
	if err != nil || !strings.Contains(out, "Acknowledged the unusual spend (a pricier model): it leaves the findings.") {
		t.Fatalf("ack: %v %q", err, out)
	}
}
