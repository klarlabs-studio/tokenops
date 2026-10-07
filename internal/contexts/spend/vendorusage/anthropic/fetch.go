package anthropic

import (
	"context"
	"fmt"
)

// maxPages bounds one fetch. A 31-day window of hourly buckets is 744
// buckets, 31 pages at the 24-bucket default; a fetch that runs past this
// is a response that never ends, not a long window.
const maxPages = 1000

// FetchAll reads every page of req's usage report: the Admin API returns
// 24 hourly buckets a page by default and says so with has_more and
// next_page, which the client leaves to its caller. Read once, a 168-hour
// window came back as its first day.
//
// It returns the buckets read before an error, and an error when a page
// names a page already read, rather than loop.
func FetchAll(ctx context.Context, c UsageReporter, req MessagesUsageRequest) ([]UsageBucket, error) {
	var out []UsageBucket
	seen := map[string]bool{req.Page: true}
	for range maxPages {
		resp, err := c.MessagesUsage(ctx, req)
		if err != nil {
			return out, err
		}
		out = append(out, resp.Data...)
		if !resp.HasMore || resp.NextPage == nil || *resp.NextPage == "" {
			return out, nil
		}
		if seen[*resp.NextPage] {
			return out, fmt.Errorf("anthropic admin: usage report named page %q twice", *resp.NextPage)
		}
		seen[*resp.NextPage] = true
		req.Page = *resp.NextPage
		if err := ctx.Err(); err != nil {
			return out, err
		}
	}
	return out, fmt.Errorf("anthropic admin: usage report ran past %d pages", maxPages)
}
