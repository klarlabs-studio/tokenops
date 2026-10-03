// Package glossary says, in plain words, what each figure TokenOps shows
// means: what it measures, how it is worked out, and how to read it.
// The terminal's `tokenops explain` and the MCP tool both answer from
// here, so a person and an agent get the same explanation.
package glossary

import (
	"fmt"
	"sort"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

// Term is one explained figure.
type Term struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	// Area is where it shows: dx, spend, headroom, statusline.
	Area string `json:"area"`
	// Short is one line, for the list.
	Short string `json:"short"`
	// What it measures, How it is worked out, and how to Read it.
	What string `json:"what"`
	How  string `json:"how"`
	Read string `json:"read"`
	// Grades are the dx grade bands, when the figure is graded.
	Grades string `json:"grades,omitempty"`
	// Where names the commands that show it.
	Where string `json:"where"`
}

// bands words a dx threshold as grade bands.
func bands(t agentdx.Threshold, unit string) string {
	f := func(v float64) string {
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", v), "0"), ".") + unit
	}
	if t.HigherIsBetter {
		return fmt.Sprintf("A at %s or more · B from %s · C from %s · F below %s", f(t.Green), f(t.Yellow), f(t.Red), f(t.Red))
	}
	return fmt.Sprintf("A up to %s · B up to %s · C up to %s · F above %s", f(t.Green), f(t.Yellow), f(t.Red), f(t.Red))
}

// terms is the glossary, in the order `tokenops explain` lists it.
func terms() []Term {
	th := agentdx.DefaultThresholds
	return []Term{
		{Name: "instruction", Aliases: []string{"prompt", "instructions"}, Area: "dx",
			Short: "one thing you asked, and everything the agent did until you asked the next",
			What:  "The unit every dx figure is measured in: a prompt you typed, and all the turns, tool calls and edits the agent made before your next one.",
			How:   "Read from the agent's own transcripts (Claude Code, Codex, Cursor, opencode). Sessions in throwaway directories are left out.",
			Read:  "Figures are per instruction, so they compare across sessions of any length.",
			Where: "tokenops dx"},
		{Name: "turns", Aliases: []string{"turns-per-instruction", "turns (median)"}, Area: "dx",
			Short:  "how many model replies a typical instruction takes",
			What:   "The number of model turns (one reply, often with tool calls) an instruction needs before the agent stops.",
			How:    "The median across instructions in the window, so a few marathons do not hide what most requests feel like. p90 is the slowest tenth.",
			Read:   "Lower is less back-and-forth. A high p90 with a low median means most work is quick and a few instructions drag: those are the ones to look at.",
			Grades: bands(th.Turns, " turns"), Where: "tokenops dx"},
		{Name: "wall-clock", Aliases: []string{"wall clock", "time", "duration", "seconds"}, Area: "dx",
			Short:  "how long you wait for a typical instruction to finish",
			What:   "The time from when you sent an instruction to the agent's last turn on it.",
			How:    "Taken from transcript timestamps; the median across instructions, with p90 as the slow tail. It includes the model thinking, tools running and any waiting on you for approvals.",
			Read:   "Turns measure the agent's effort, wall-clock measures your waiting; they diverge when each turn is slow. A long p90 with a short median is a few long jobs, not a slow agent.",
			Grades: bands(th.Duration, "s"), Where: "tokenops dx"},
		{Name: "tokens", Aliases: []string{"tokens per instruction", "tokens (median)"}, Area: "dx",
			Short: "how much context a typical instruction reads, summed over its turns",
			What:  "The input the model read for an instruction, added up across its turns, cache reads included.",
			How:   "Summed per instruction, then the median.",
			Read:  "It grows with the size of the context each turn re-reads, so it rises in long sessions even when the instructions are small. Cached tokens are cheap but still count here.",
			Where: "tokenops dx"},
		{Name: "tool-calls", Aliases: []string{"tool calls"}, Area: "dx",
			Short: "how many tools a typical instruction runs",
			What:  "File reads, edits, searches and commands the agent ran for an instruction.",
			How:   "Counted per instruction, then the median.",
			Read:  "Neither good nor bad on its own: a careful agent reads before it edits. Look at it next to rework and repeated.",
			Where: "tokenops dx"},
		{Name: "context-growth", Aliases: []string{"context growth", "context growth/turn"}, Area: "dx",
			Short:  "how much the context grows from one turn to the next",
			What:   "The typical increase in context size between consecutive turns.",
			How:    "The median of turn-to-turn increases.",
			Read:   "Fast growth is what forces a compaction. File dumps and long command output drive it; tokenops fmt and read-guard keep it down.",
			Grades: bands(th.ContextGrowth, " tokens"), Where: "tokenops dx"},
		{Name: "first-try", Aliases: []string{"first try", "first-try rate"}, Area: "dx",
			Short:  "share of instructions done without rework, interruption or delegation",
			What:   "Instructions the agent completed without revising a file it had already edited for them, without you stopping it, and without handing it to a subagent.",
			How:    "Those instructions as a share of all instructions in the window.",
			Read:   "The closest single number to \"it just worked\". Higher is better.",
			Grades: bands(th.FirstTry, "%"), Where: "tokenops dx"},
		{Name: "rework", Aliases: []string{"rework rate"}, Area: "dx",
			Short:  "share of edits that revise a file already edited for the same instruction",
			What:   "Edits that go back to a file the agent already changed while answering the same instruction: revising itself rather than getting it right.",
			How:    "Those edits as a share of all edits. Shown as n/a when there were no edits, which is not the same as no rework.",
			Read:   "Lower is better. Persistent rework often means the instruction was underspecified.",
			Grades: bands(th.Rework, "%"), Where: "tokenops dx"},
		{Name: "interrupt", Aliases: []string{"interrupt rate"}, Area: "dx",
			Short:  "share of instructions you had to stop",
			What:   "Instructions you interrupted before the agent finished.",
			How:    "Read from the interruption markers the agents write.",
			Read:   "Lower is better. A rising rate means the agent is heading the wrong way often enough that you step in.",
			Grades: bands(th.Interrupt, "%"), Where: "tokenops dx"},
		{Name: "escalation", Aliases: []string{"escalation rate", "delegation"}, Area: "dx",
			Short:  "share of instructions handed to a subagent",
			What:   "Instructions for which the agent started a subagent.",
			How:    "Read from the subagent calls in the transcript.",
			Read:   "Some delegation is healthy; a high share means work is often too big for one context.",
			Grades: bands(th.Escalation, "%"), Where: "tokenops dx"},
		{Name: "compactions", Aliases: []string{"compactions/session", "compaction"}, Area: "dx",
			Short:  "how often a session's context is summarized to make room",
			What:   "Times per session the agent compacted its context, by you (/compact) or on its own.",
			How:    "Counted from the compaction records in the transcript.",
			Read:   "Compaction loses detail. Frequent compactions mean long sessions or fast context growth.",
			Grades: bands(th.Compaction, ""), Where: "tokenops dx"},
		{Name: "overall", Aliases: []string{"overall grade", "grade"}, Area: "dx",
			Short: "the worst grade among the dx figures",
			What:  "The lowest letter across all graded figures, with the figure that set it.",
			How:   "The minimum, not an average.",
			Read:  "An experience is only as good as its sharpest friction, so the overall names the one thing most worth fixing.",
			Where: "tokenops dx"},
		{Name: "repeated", Area: "dx",
			Short: "share of tool calls the agent already made, with the same arguments",
			What:  "The agent re-issuing a call it made shortly before, a sign it lost track of what it had done.",
			How:   "Counted against the session's last 50 calls, so a long session cannot inflate it.",
			Read:  "Lower is better. It tends to rise as context fills.",
			Where: "tokenops dx (quality vs context)"},
		{Name: "rejected", Area: "dx",
			Short: "share of instructions where you said the reply was wrong",
			What:  "Your next message rejecting the answer before it (\"no\", \"that's wrong\", undo requests).",
			How:   "Matched on short, unambiguous phrases.",
			Read:  "Lower is better. Compare it across context sizes to see whether quality drops in long sessions.",
			Where: "tokenops dx"},
		{Name: "peak-context", Aliases: []string{"peak ctx"}, Area: "dx",
			Short: "the largest single turn's context for a typical instruction",
			What:  "How full the context was on an instruction's biggest turn.",
			How:   "The largest turn per instruction, then the median.",
			Read:  "Shows how close typical work runs to the window.",
			Where: "tokenops dx (by model and effort)"},
		{Name: "effort", Aliases: []string{"reasoning effort", "variant"}, Area: "dx",
			Short: "the reasoning effort a model ran at (low, medium, high…)",
			What:  "The effort level set for the model on each turn, where the agent records it.",
			How:   "Read per turn; an instruction takes the level most of its turns used.",
			Read:  "Compare levels within one model and only with 30+ instructions each. A higher level doing worse may mean harder instructions, not a worse setting.",
			Where: "tokenops dx (by model and effort)"},

		{Name: "total-spend", Aliases: []string{"total spend", "cost", "spend"}, Area: "spend",
			Short: "what usage billed per token cost",
			What:  "Money for usage you pay per token: API keys, pay-as-you-go gateways, usage-based Enterprise.",
			How:   "Each request priced at its biller's rate card, or the cost the vendor measured. Usage a subscription covers counts as $0 here.",
			Read:  "On a flat-rate plan this is near $0 by design; look at api-equivalent and plan cost instead.",
			Where: "tokenops spend"},
		{Name: "api-equivalent", Aliases: []string{"api equivalent", "value"}, Area: "spend",
			Short: "what all usage would cost at list prices",
			What:  "Every request priced at its biller's public rate, whether or not a plan covered it.",
			How:   "Tokens times the list rate, per model and biller.",
			Read:  "On a subscription it is what the work was worth, not what you paid. In the statusline it is marked \"value\".",
			Where: "tokenops spend, statusline"},
		{Name: "plan-cost", Aliases: []string{"plans", "plan cost"}, Area: "spend",
			Short: "what your subscriptions cost over the window",
			What:  "Your plans' monthly prices, prorated by day across the window and any switches.",
			How:   "Your recorded price (plan set --price), else the plan's US list price. Plans billed per token have no flat fee and are left out.",
			Read:  "Compare it with api-equivalent: value per plan is the ratio.",
			Where: "tokenops spend, tokenops plan history"},
		{Name: "value-per-plan", Aliases: []string{"value per plan"}, Area: "spend",
			Short: "how many times its price your subscriptions returned",
			What:  "api-equivalent divided by plan cost, in your currency.",
			How:   "Shown only when the plans cost at least one unit of currency in the window.",
			Read:  "Above 1x the plan paid for itself at list prices.",
			Where: "tokenops spend"},
		{Name: "burn-rate", Aliases: []string{"burn rate"}, Area: "spend",
			Short: "spend and tokens over the last 24 hours",
			What:  "What the last day cost, and how many tokens it used.",
			How:   "The last 24 hours of priced usage, with an hourly series.",
			Read:  "On a subscription the money is $0 at the margin, so the tokens are the burn.",
			Where: "tokenops spend"},
		{Name: "exchange-rate", Aliases: []string{"rate", "currency"}, Area: "spend",
			Short: "the rate used to show dollars in your currency",
			What:  "Usage is priced in US dollars, as vendors publish it, and converted to your currency.",
			How:   "The ECB's daily reference rate, fetched at most once a day, or a rate you pin (money.per_usd).",
			Read:  "A converted amount moves with the exchange rate even when usage does not.",
			Where: "tokenops spend, statusline"},
		{Name: "biller", Aliases: []string{"provider"}, Area: "spend",
			Short: "whose bill a request counts against",
			What:  "The company that bills a request: Anthropic, OpenAI, or a gateway such as Fireworks or OpenRouter.",
			How:   "From the endpoint the agent sends to and the model that answered (ADR 0009). A gateway bills its own models; through FireRouter, Claude turns run on your Anthropic API key.",
			Read:  "Spend, plans and limits are kept per biller, so a gateway's turns never land on your Anthropic plan.",
			Where: "tokenops spend --by provider"},
		{Name: "plan-covered", Aliases: []string{"plan covered", "covered"}, Area: "spend",
			Short: "usage a subscription pays for",
			What:  "Requests through a vendor's own endpoint with a subscription bound to it.",
			How:   "A plan covers only its vendor's own endpoint: a turn through a gateway runs on an API key and is billed.",
			Read:  "Covered usage costs $0 at the margin and counts against the plan's windows instead.",
			Where: "tokenops spend, tokenops plan headroom"},

		{Name: "window", Aliases: []string{"5h", "wk", "quota", "quota window"}, Area: "headroom",
			Short: "how much of a plan's usage window is used",
			What:  "Subscriptions limit usage per window: 5 hours and a week for Claude and ChatGPT plans.",
			How:   "The vendor's own reading when available (Claude Code, the Claude usage meter, Codex), otherwise counted from your usage.",
			Read:  "Green below 60%, amber from 60%, red from 80%. ↻ is when it resets.",
			Where: "tokenops plan headroom, statusline"},
		{Name: "spend-limit", Aliases: []string{"spend limit", "limit"}, Area: "headroom",
			Short: "spend against a limit, for plans billed per token",
			What:  "For usage-based Enterprise or a pay-as-you-go account, the money spent this period against the limit set for it.",
			How:   "The vendor's figures when TokenOps can read them (the Claude usage meter), otherwise priced from your usage against the limit you recorded.",
			Read:  "Limits are dated, so each month is measured against its own.",
			Where: "tokenops plan headroom"},
		{Name: "signal-quality", Aliases: []string{"signal quality", "signal"}, Area: "headroom",
			Short: "how far a headroom figure can be trusted",
			What:  "Which source the reading came from.",
			How:   "High when the vendor reported it or the agent's own logs fed it; low when only TokenOps' activity pings did.",
			Read:  "Treat a low-signal figure as a hint, not a meter.",
			Where: "tokenops plan headroom"},

		{Name: "ctx", Aliases: []string{"context", "compact point", "ctx → compact"}, Area: "statusline",
			Short: "how full the context is, against where the session compacts",
			What:  "The context used, and (after →) the point your coach settings compact at.",
			How:   "Claude Code reports the fill; the compaction point comes from the coach's context setting.",
			Read:  "Coloured against the compaction point, not 100%, so it turns red before compaction happens.",
			Where: "statusline"},
		{Name: "cache", Aliases: []string{"cache hit", "prompt cache"}, Area: "statusline",
			Short: "share of input served from the prompt cache",
			What:  "How much of what the model read came from the prompt cache.",
			How:   "As Claude Code reports it.",
			Read:  "Higher is cheaper and faster. It drops after a pause long enough for the cache to expire.",
			Where: "statusline"},
	}
}

// List is every term, in order.
func List() []Term { return terms() }

// Lookup finds a term by name or alias, ignoring case, spaces and
// dashes. ok is false when nothing matches; suggestions then name the
// closest terms.
func Lookup(query string) (t Term, ok bool, suggestions []string) {
	key := norm(query)
	all := terms()
	for _, t := range all {
		if norm(t.Name) == key {
			return t, true, nil
		}
		for _, a := range t.Aliases {
			if norm(a) == key {
				return t, true, nil
			}
		}
	}
	for _, t := range all {
		if strings.Contains(norm(t.Name), key) || strings.Contains(norm(t.Short), key) {
			suggestions = append(suggestions, t.Name)
		}
	}
	sort.Strings(suggestions)
	return Term{}, false, suggestions
}

func norm(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer("-", "", " ", "", "_", "", "(", "", ")", "").Replace(s)
}
