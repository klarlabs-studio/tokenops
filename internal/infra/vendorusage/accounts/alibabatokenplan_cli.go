package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// readerAlibabaTokenPlanCLI registers the reader (readers_gen.go): Alibaba
// Cloud's Bailian CLI, run as its descriptor says with only the
// environment it needs, as CodexBar's Alibaba Token Plan provider runs it
// (AlibabaTokenPlanCLIUsageFetcher.swift, 53f9e1e24d). A Personal or Solo
// plan's windows come from `bl console call` on the personal usage API,
// parsed as the console's are. A Team plan has no such windows: its seats'
// credits are listed page by page (`bl token-plan list-seats`).
func readerAlibabaTokenPlanCLI() usage.Reader {
	return alibabaTokenPlanCLI{cliSource: cliSource{provider: "alibabatokenplan", tag: "alibabatokenplan-cli", parse: parseTokenPlanCLI}}
}

func parseTokenPlanCLI(out string) (usage.Reading, error) {
	return parseTokenPlanWindows([]byte(jsonPart(out)))
}

// alibabaTokenPlanCLI is the personal read (cliSource), then the Team one.
type alibabaTokenPlanCLI struct {
	cliSource
	// now is the clock; nil uses time.Now.
	now func() time.Time
}

// tokenPlanTeamHosts are the model API hosts list-seats picks its region
// from (BAILIAN_BASE_URL), international first. They are TokenOps' own,
// never an endpoint or credential from the environment.
var tokenPlanTeamHosts = []string{"https://dashscope-intl.aliyuncs.com", "https://dashscope.aliyuncs.com"}

const (
	tokenPlanSeatPage = 10
	tokenPlanMaxSeats = 1000
)

func (a alibabaTokenPlanCLI) Read(ctx context.Context, key string) (usage.Reading, error) {
	r, err := a.cliSource.Read(ctx, key)
	if err == nil || errors.Is(err, usage.ErrNotInstalled) || errors.Is(err, usage.ErrAuth) {
		return r, err
	}
	src, ok := providers.ForSource(a.tag)
	if !ok || src.Command == nil {
		return usage.Reading{}, err
	}
	bin, found := a.bin, a.bin != ""
	if !found {
		bin, found = locateCLI(src.Command.Binary, "")
	}
	if !found {
		return usage.Reading{}, usage.ErrNotInstalled
	}
	now := time.Now
	if a.now != nil {
		now = a.now
	}
	for _, host := range tokenPlanTeamHosts {
		env := append(allowedEnv(src.Command.EnvAllow), "BAILIAN_BASE_URL="+host, "DASHSCOPE_BASE_URL="+host)
		team, terr := tokenPlanTeam(ctx, bin, env, src.Command.Timeout, now())
		if terr == nil {
			return team, nil
		}
	}
	return usage.Reading{}, err
}

type tokenPlanSeatsPage struct {
	Success bool `json:"Success"`
	Data    struct {
		Items []struct {
			SeatID   string   `json:"SeatId"`
			Status   string   `json:"Status"`
			Start    *float64 `json:"StartTime"`
			End      *float64 `json:"EndTime"`
			Equities []struct {
				Type      string   `json:"EquityType"`
				Start     *float64 `json:"CycleStartTime"`
				End       *float64 `json:"CycleEndTime"`
				Total     *float64 `json:"CycleTotalValue"`
				Remaining *float64 `json:"CycleSurplusValue"`
			} `json:"EquityList"`
		} `json:"Items"`
		Total    int `json:"Total"`
		PageNo   int `json:"PageNo"`
		PageSize int `json:"PageSize"`
	} `json:"Data"`
}

var errTokenPlanSeats = errors.New("accounts: bl token-plan list-seats: unrecognised output")

// tokenPlanTeam lists the Team plan's seats page by page within one
// deadline and adds up the current credits of the seats in service: the
// share used of their total, resetting when every seat's cycle ends at the
// same time.
func tokenPlanTeam(ctx context.Context, bin string, env []string, timeout time.Duration, now time.Time) (usage.Reading, error) {
	if timeout <= 0 {
		timeout = cliTimeout
	}
	deadline := time.Now().Add(timeout)
	var pages []tokenPlanSeatsPage
	received := 0
	for len(pages) < tokenPlanMaxSeats/tokenPlanSeatPage {
		left := time.Until(deadline)
		if left <= 0 {
			return usage.Reading{}, errors.New("accounts: bl token-plan list-seats did not answer in time")
		}
		pageNo := len(pages) + 1
		out, err := runCommand(ctx, left, bin, env, "token-plan", "list-seats", "--page-no", strconv.Itoa(pageNo),
			"--page-size", strconv.Itoa(tokenPlanSeatPage), "--output", "json")
		if err != nil {
			return usage.Reading{}, err
		}
		var p tokenPlanSeatsPage
		if json.Unmarshal([]byte(jsonPart(out)), &p) != nil || !p.Success || p.Data.PageNo != pageNo ||
			p.Data.PageSize != tokenPlanSeatPage || p.Data.Total < 0 || p.Data.Total > tokenPlanMaxSeats ||
			len(p.Data.Items) > p.Data.PageSize {
			return usage.Reading{}, errTokenPlanSeats
		}
		received += len(p.Data.Items)
		if received > p.Data.Total || (len(pages) > 0 && pages[0].Data.Total != p.Data.Total) ||
			(received != p.Data.Total && len(p.Data.Items) != tokenPlanSeatPage) {
			return usage.Reading{}, errTokenPlanSeats
		}
		pages = append(pages, p)
		if received == p.Data.Total {
			return tokenPlanTeamReading(pages, now)
		}
	}
	return usage.Reading{}, errTokenPlanSeats
}

// tokenPlanTeamReading adds up the seats in service now: each must have
// exactly one current CREDITS equity; resource packs and past cycles are
// not added.
func tokenPlanTeamReading(pages []tokenPlanSeatsPage, now time.Time) (usage.Reading, error) {
	ms := float64(now.UnixMilli())
	seen := map[string]bool{}
	var total, remaining float64
	ends := map[float64]bool{}
	for _, p := range pages {
		for _, s := range p.Data.Items {
			if s.SeatID == "" || seen[s.SeatID] {
				return usage.Reading{}, errTokenPlanSeats
			}
			seen[s.SeatID] = true
			if s.Status != "NORMAL" {
				continue
			}
			if s.Start == nil || s.End == nil || *s.Start <= 0 || *s.End <= *s.Start {
				return usage.Reading{}, errTokenPlanSeats
			}
			if *s.Start > ms || ms >= *s.End {
				continue
			}
			var credits int
			for _, e := range s.Equities {
				if e.Type != "CREDITS" {
					continue
				}
				credits++
				if e.Start == nil || e.End == nil || e.Total == nil || e.Remaining == nil || *e.Start <= 0 ||
					*e.Start > ms || ms >= *e.End || *e.Total < 0 || *e.Remaining < 0 || *e.Remaining > *e.Total {
					return usage.Reading{}, errTokenPlanSeats
				}
				total += *e.Total
				remaining += *e.Remaining
				ends[*e.End] = true
			}
			if credits != 1 {
				return usage.Reading{}, errTokenPlanSeats
			}
		}
	}
	if total <= 0 {
		return usage.Reading{}, fmt.Errorf("%w (no seat in service has credits)", errTokenPlanSeats)
	}
	w := usage.Window{Name: "credits", UsedPct: clampPct(pct(total-remaining, total))}
	if len(ends) == 1 {
		for end := range ends {
			w.ResetsAt = time.UnixMilli(int64(end)).UTC()
		}
	}
	return usage.Reading{Scope: "team", Subscription: true, Windows: []usage.Window{w}}, nil
}
