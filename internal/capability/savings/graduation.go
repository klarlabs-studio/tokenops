package savings

import (
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/contexts/intervention"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
)

// Step is one capability's graduation (#662): where it stands on the
// shadow → advise → act ladder, the published bar for the next stage,
// the evidence against it, and the command that promotes it. Nothing
// promotes on its own; Promote is for the operator.
type Step struct {
	learning.Graduation
	// Power is the coach power whose rung sets the stage.
	Power string `json:"power"`
	// Promote is the command that takes the next step, when there is one.
	Promote  string                      `json:"promote,omitempty"`
	Evidence learning.GraduationEvidence `json:"evidence"`
}

// capabilityPower pairs each graduated capability with its coach power,
// in reporting order.
var capabilityPower = []struct{ capability, power string }{
	{learning.CapabilityReadGuard, config.PowerWaste},
	{learning.CapabilityCompaction, config.PowerContext},
	{learning.CapabilitySessionBudget, config.PowerInform},
	{learning.CapabilityRouting, config.PowerModels},
}

// actRung is the rung that makes a power act.
var actRung = map[string]string{
	config.PowerWaste: config.AutonomyAutonomous, config.PowerContext: config.AutonomyAutonomous,
	config.PowerModels: config.AutonomyAsk,
}

// Graduations reads each capability's evidence off the savings report
// and the follow-through summaries, at the stage effective puts it on
// (effective returns a power's effective rung).
func Graduations(r Report, effective func(power string) string, followThrough []ft.Summary) []Step {
	out := make([]Step, 0, len(capabilityPower))
	for _, cp := range capabilityPower {
		ev := evidence(cp.capability, r, followThrough)
		g := learning.Graduate(cp.capability, learning.StageOf(effective(cp.power)), ev)
		out = append(out, Step{Graduation: g, Power: cp.power, Promote: promote(cp.power, g.Next), Evidence: ev})
	}
	return out
}

func evidence(capability string, r Report, followThrough []ft.Summary) learning.GraduationEvidence {
	var ev learning.GraduationEvidence
	var realized []measurement.Value
	var tokens int64
	for _, k := range r.Kinds {
		if k.Capability != capability {
			continue
		}
		if capability == learning.CapabilityReadGuard {
			// The guard calls a re-read redundant only for a file read in
			// full and unchanged since (size and mtime), so each shadow
			// call is right by construction; what it would have saved is
			// the re-read that went ahead.
			ev.Shadow += k.Shadow.Count
			ev.ShadowConfirmed += k.Shadow.Count
			tokens += k.ShadowTokens
			continue
		}
		// A held or observed tip was right when the session did not fix
		// what it flagged on its own.
		resolved := k.Holdout.Resolved + k.Shadow.Resolved
		ev.Shadow += resolved
		ev.ShadowConfirmed += resolved - k.Holdout.Followed - k.Shadow.Followed
		if k.RealizedUSD.Known() {
			realized = append(realized, k.RealizedUSD)
		}
		if k.Verdict.Outcome == intervention.Harmed {
			ev.Harmed = true
		}
	}
	switch {
	case capability == learning.CapabilityReadGuard && tokens > 0:
		ev.Realized, ev.RealizedUnit = measurement.Estimated(float64(tokens), "read_guard_shadow",
			"re-reads that went ahead, sized from the file"), "tokens"
	case capability == learning.CapabilityReadGuard:
		ev.Realized, ev.RealizedUnit = measurement.Unknown("no redundant re-read watched yet"), "tokens"
	case len(realized) > 0:
		ev.Realized, ev.RealizedUnit = measurement.Sum(realized...), "usd"
	case capability == learning.CapabilityRouting:
		ev.Realized, ev.RealizedUnit = measurement.Unknown("routing advice is not held back; `tokenops experiment start` gives it randomized evidence"), "usd"
	default:
		ev.Realized, ev.RealizedUnit = measurement.Unknown("no holdout comparison has concluded"), "usd"
	}
	for _, s := range followThrough {
		if summaryCapability(s) != capability {
			continue
		}
		ev.Advised += s.Followed + s.Ignored + s.Stood + s.Undone + s.Acknowledged + s.Expected + s.Snoozed
		ev.Accepted += s.Followed + s.Stood + s.Acknowledged
	}
	return ev
}

// summaryCapability is the capability a follow-through kind belongs to.
func summaryCapability(s ft.Summary) string {
	switch {
	case s.Power == config.PowerModels:
		return learning.CapabilityRouting
	case s.Channel == ft.ChannelTip:
		return learning.CapabilityOf(s.Kind)
	}
	return ""
}

func promote(power string, next learning.Stage) string {
	switch next {
	case learning.StageAdvise:
		return "tokenops coach set " + power + " " + config.AutonomyAdvise
	case learning.StageAct:
		if rung, ok := actRung[power]; ok {
			return "tokenops coach set " + power + " " + rung
		}
		return "the session budget acts through an action boundary: write one with session_cost_usd and `tokenops boundaries promote` it"
	}
	return ""
}

var capabilityWords = map[string]string{
	learning.CapabilityReadGuard: "read guard", learning.CapabilityCompaction: "compact tip",
	learning.CapabilitySessionBudget: "session budget", learning.CapabilityRouting: "model routing",
}

// CapabilityName is how a graduated capability reads in a sentence.
func CapabilityName(capability string) string {
	if n, ok := capabilityWords[capability]; ok {
		return n
	}
	return capability
}

var nextWords = map[learning.Stage]string{learning.StageAdvise: "advise", learning.StageAct: "act"}

// Headline is a ready step in one line, for an ambient surface; empty
// when the step is not ready.
func Headline(s Step) string {
	if !s.Ready {
		return ""
	}
	verb := nextWords[s.Next]
	if s.Capability == learning.CapabilityReadGuard && s.Next == learning.StageAct {
		verb = "enforce"
	}
	return fmt.Sprintf("%s is ready to %s", capabilityWords[s.Capability], verb)
}

// Bar is the criteria in one line: each with where the evidence stands
// against it.
func Bar(s Step) string {
	parts := make([]string, 0, len(s.Criteria))
	for _, c := range s.Criteria {
		mark := "✗"
		if c.Met {
			mark = "✓"
		}
		parts = append(parts, fmt.Sprintf("%s %s: %s (needs %s)", mark, c.Name, c.Have, c.Want))
	}
	return strings.Join(parts, " · ")
}
