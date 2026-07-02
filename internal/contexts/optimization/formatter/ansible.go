package formatter

import (
	"fmt"
	"strings"
)

// Ansible compresses the output of `ansible-playbook` runs. Playbook output
// is extremely noisy: every play emits a "PLAY [name] ***" banner, every
// task a "TASK [name] ***" banner, and each task reports one line per host
// ("ok: [host]", "changed: [host]", "skipping: [host]"). A large inventory
// running an idempotent playbook produces a wall of "ok:" lines an agent
// never acts on. The signal is the opposite: task/host failures ("failed:",
// "fatal: … FAILED!", "… UNREACHABLE!"), state changes ("changed:"), and the
// final "PLAY RECAP" block that tallies per-host ok/changed/unreachable/failed
// counts — the one place a host with failures is summarised.
//
//   - Failures and unreachable hosts are always kept (they are the reason to
//     read the output at all).
//   - The full PLAY RECAP survives at every level: its header is critical and
//     a recap host line with any failure or unreachable count is critical;
//     healthy recap lines are kept for context alongside them.
//   - "ok:" and "skipping:" per-host lines are pure idempotency noise dropped
//     at Balanced+. "changed:" lines carry state an agent cares about and are
//     kept at Balanced, then dropped at Aggressive (the recap still summarises
//     the change counts). At Aggressive a TASK header whose children were all
//     dropped is itself dropped so empty banners do not survive.
//
// Output that does not resemble ansible is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type Ansible struct{}

// NewAnsible returns the ansible formatter.
func NewAnsible() *Ansible { return &Ansible{} }

// Command reports the canonical ansible command token.
func (a *Ansible) Command() string { return "ansible-playbook" }

// Aliases registers the formatter for the bare `ansible` ad-hoc command as
// well as the canonical `ansible-playbook`.
func (a *Ansible) Aliases() []string { return []string{"ansible"} }

// CriticalLine treats failure signal and the run summary as critical: any
// "failed:" or "fatal:" host line, any line announcing a FAILED! or
// UNREACHABLE! condition, the "PLAY RECAP" header, and a recap host line that
// records a non-zero failed= or unreachable= count. Healthy per-host results
// ("ok:", "changed:", "skipping:") are never critical.
func (a *Ansible) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "failed:") || strings.HasPrefix(t, "fatal:") {
		return true
	}
	if strings.Contains(t, "FAILED!") || strings.Contains(t, "UNREACHABLE!") {
		return true
	}
	if strings.HasPrefix(t, "PLAY RECAP") {
		return true
	}
	// Recap host line: critical when it records any failure or any host it
	// could not reach. The "…=0" guard keeps a healthy host out of the set.
	if strings.Contains(t, "failed=") && !strings.Contains(t, "failed=0") {
		return true
	}
	if strings.Contains(t, "unreachable=") && !strings.Contains(t, "unreachable=0") {
		return true
	}
	return false
}

// looksLikeAnsible reports whether b resembles ansible-playbook output — a
// PLAY/TASK banner, the recap header, a per-host result line, or the literal
// command token.
func looksLikeAnsible(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "PLAY [") ||
		strings.Contains(s, "TASK [") ||
		strings.Contains(s, "PLAY RECAP") ||
		strings.Contains(s, "ok:") ||
		strings.Contains(s, "changed:") ||
		strings.Contains(s, "ansible")
}

// isAnsiblePlayHeader reports a "PLAY [name] ***" banner (not the recap,
// which begins "PLAY RECAP").
func isAnsiblePlayHeader(t string) bool {
	return strings.HasPrefix(t, "PLAY [")
}

// isAnsibleTaskHeader reports a "TASK [name] ***" banner.
func isAnsibleTaskHeader(t string) bool {
	return strings.HasPrefix(t, "TASK [")
}

// Format compresses ansible output; non-ansible output falls back to generic.
func (a *Ansible) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeAnsible(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "ansible: non-ansible output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(a, raw, scrubbed, 0, "ansible: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	inRecap := false // once true, keep the whole PLAY RECAP block for context

	// pendingTask buffers a TASK header at Aggressive so a task whose child
	// lines were all dropped leaves no empty banner behind.
	pendingTask := ""
	havePending := false
	emitPending := func() {
		if havePending {
			kept = append(kept, pendingTask)
			havePending = false
		}
	}
	dropPending := func() {
		if havePending {
			havePending = false
			dropped++
		}
	}

	for _, line := range lines {
		t := strings.TrimSpace(line)

		// The recap block is kept verbatim (its header and failing hosts are
		// critical; healthy hosts ride along for context).
		if inRecap {
			if t == "" {
				dropped++
				continue
			}
			kept = append(kept, line)
			continue
		}
		if strings.HasPrefix(t, "PLAY RECAP") {
			dropPending()
			inRecap = true
			kept = append(kept, line)
			continue
		}

		if isAnsiblePlayHeader(t) {
			dropPending()
			kept = append(kept, line)
			continue
		}
		if isAnsibleTaskHeader(t) {
			// A new task header supersedes an unflushed previous one.
			dropPending()
			if level == LossAggressive {
				pendingTask = line
				havePending = true
			} else {
				kept = append(kept, line)
			}
			continue
		}

		// Failure signal always survives.
		if a.CriticalLine(t) {
			emitPending()
			kept = append(kept, line)
			continue
		}

		// Idempotency noise: dropped at Balanced+.
		if strings.HasPrefix(t, "ok:") || strings.HasPrefix(t, "skipping:") {
			dropped++
			continue
		}

		// State changes: kept at Balanced, dropped at Aggressive.
		if strings.HasPrefix(t, "changed:") {
			if level == LossAggressive {
				dropped++
				continue
			}
			emitPending()
			kept = append(kept, line)
			continue
		}

		if t == "" {
			dropped++
			continue
		}

		// Any other line (task-level messages, warnings) is kept.
		emitPending()
		kept = append(kept, line)
	}
	// An unflushed trailing task header had no surviving children — drop it.
	dropPending()

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("ansible-playbook: %s, %d lines dropped", level, dropped)
	res := enforceCritical(a, raw, compact, dropped, notes)
	return res, true
}
