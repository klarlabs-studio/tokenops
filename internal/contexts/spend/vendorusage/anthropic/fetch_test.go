package anthropic

import (
	"context"
	"testing"
	"time"
)

// pagedReporter answers in pages: page "" is the first, then each page's
// next_page names the following one.
type pagedReporter struct {
	pages map[string]MessagesUsageResponse
	asked []string
}

func (p *pagedReporter) MessagesUsage(_ context.Context, req MessagesUsageRequest) (*MessagesUsageResponse, error) {
	p.asked = append(p.asked, req.Page)
	r := p.pages[req.Page]
	return &r, nil
}

func bucketAt(h int) UsageBucket {
	start := time.Date(2026, 10, 1, h, 0, 0, 0, time.UTC)
	return UsageBucket{StartingAt: start, EndingAt: start.Add(time.Hour),
		Results: []UsageResult{{Model: "claude-sonnet-5", UncachedInputTokens: 10, OutputTokens: 1}}}
}

// The Admin API returns 24 hourly buckets a page by default. Reading the
// first page only, a 168-hour backfill stored one day and reported seven.
func TestFetchAllFollowsEveryPage(t *testing.T) {
	two, three := "p2", "p3"
	r := &pagedReporter{pages: map[string]MessagesUsageResponse{
		"":   {Data: []UsageBucket{bucketAt(0), bucketAt(1)}, HasMore: true, NextPage: &two},
		"p2": {Data: []UsageBucket{bucketAt(2)}, HasMore: true, NextPage: &three},
		"p3": {Data: []UsageBucket{bucketAt(3)}},
	}}
	got, err := FetchAll(context.Background(), r, MessagesUsageRequest{BucketWidth: BucketWidthHour})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || len(r.asked) != 3 {
		t.Errorf("%d buckets over %d requests (pages %q), want 4 over 3", len(got), len(r.asked), r.asked)
	}
}

// A response that names its own page again cannot loop the fetch.
func TestFetchAllStopsOnARepeatedPage(t *testing.T) {
	same := "p2"
	r := &pagedReporter{pages: map[string]MessagesUsageResponse{
		"":   {Data: []UsageBucket{bucketAt(0)}, HasMore: true, NextPage: &same},
		"p2": {Data: []UsageBucket{bucketAt(1)}, HasMore: true, NextPage: &same},
	}}
	got, err := FetchAll(context.Background(), r, MessagesUsageRequest{})
	if err == nil || len(r.asked) != 2 || len(got) != 2 {
		t.Errorf("buckets %d, requests %q, err %v; want both pages read and an error", len(got), r.asked, err)
	}
}
