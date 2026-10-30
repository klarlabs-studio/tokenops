package focus_test

import (
	"bytes"
	"encoding/csv"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/focus"
)

// A program outside this module writes the organisation dataset through
// this package alone: a blended row and the 1.4 header.
func TestWritesTheOrganisationDataset(t *testing.T) {
	row := focus.Blended(focus.Blend{Day: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC), Provider: "anthropic",
		Requests: 3, Tokens: 1_000_000, ListUSD: 5, EstimatedUSD: 1})
	var buf bytes.Buffer
	if err := focus.V1_4.WriteCSV(&buf, []focus.Row{row}); err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(&buf).ReadAll()
	if err != nil || len(recs) != 2 {
		t.Fatalf("records %d, err %v", len(recs), err)
	}
	col := map[string]string{}
	for i, name := range recs[0] {
		col[name] = recs[1][i]
	}
	if col["ListUnitPrice"] != "5" || col[focus.ColumnEstimatedCost] != "1" || col["BilledCost"] != "0" {
		t.Errorf("row = %v", col)
	}
}
