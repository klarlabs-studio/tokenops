package formatter

import (
	"fmt"
	"strings"
)

// Docker compresses the output of `docker build` (the highest-noise docker
// case, spanning both the classic builder and buildkit). The build errors
// and the final result — "ERROR" lines, "failed to solve", buildkit
// "=> ERROR" steps, and the "Successfully built" / "Successfully tagged" /
// "naming to " completion lines — are the signal an agent acts on and are
// always kept.
//
// The layer/pull progress chatter (fs-layer pulls, checksum verification,
// download/extract progress, buildkit "=> => transferring" transfer lines)
// and the intermediate-container bookkeeping ("---> Using cache",
// "---> Running in …") carry no state and are stripped at Balanced and
// above. At Aggressive the "Step N/M : …" lines collapse to the first step
// plus a count so a long multi-stage build shrinks to its result — the
// collapse uses the same size guard as the git formatter's untracked-file
// block, so it never produces output larger than the lines it replaces.
//
// Output that does not resemble docker is handed to the generic noise
// scrub, so the formatter is never destructive on commands it does not
// model.
type Docker struct{}

// NewDocker returns the docker formatter.
func NewDocker() *Docker { return &Docker{} }

// Command reports the docker command token.
func (d *Docker) Command() string { return "docker" }

// CriticalLine treats build errors and the final result as critical: any
// line beginning "ERROR" (covers "ERROR" and "ERROR:"), any line carrying
// "failed to solve" or an "error:" message, the buildkit "=> ERROR" step
// lines, the "Successfully built" / "Successfully tagged" completion lines,
// and the buildkit "naming to …" final line. Progress and layer chatter is
// never critical.
func (d *Docker) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "ERROR"),
		strings.Contains(t, "failed to solve"),
		strings.Contains(t, "error:"),
		strings.Contains(t, "=> ERROR"),
		strings.Contains(t, "Successfully built"),
		strings.Contains(t, "Successfully tagged"),
		strings.HasPrefix(t, "naming to "):
		return true
	}
	return false
}

// looksLikeDocker reports whether b resembles `docker build` output from
// either the classic builder or buildkit.
func looksLikeDocker(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Step ") ||
		strings.Contains(s, "--->") ||
		strings.Contains(s, "Pulling from") ||
		strings.Contains(s, "=> ") ||
		strings.Contains(s, "Successfully built") ||
		strings.Contains(s, "docker")
}

// isDockerStep reports the "Step N/M : …" lines the classic builder prints
// for each Dockerfile instruction. Collapsed at Aggressive.
func isDockerStep(t string) bool {
	return strings.HasPrefix(t, "Step ")
}

// isDockerLayerNoise reports the layer/pull progress and intermediate-
// container lines that carry no state and are safe to drop at Balanced+:
// fs-layer pulls, checksum verification, download/extract progress (classic
// and buildkit), and the "---> Using cache" / "---> Running in …" container
// bookkeeping.
func isDockerLayerNoise(t string) bool {
	switch {
	case strings.Contains(t, "Pulling fs layer"),
		strings.Contains(t, "Waiting"),
		strings.Contains(t, "Verifying Checksum"),
		strings.Contains(t, "Download complete"),
		strings.Contains(t, "Pull complete"),
		strings.Contains(t, "Extracting"),
		strings.Contains(t, "Downloading"),
		strings.Contains(t, "=> => transferring"),
		strings.Contains(t, "=> => sha256:"),
		strings.Contains(t, "=> => extracting"),
		strings.HasPrefix(t, "---> Using cache"),
		strings.HasPrefix(t, "---> Running in "):
		return true
	}
	return isDockerHashProgress(t)
}

// isDockerHashProgress reports a classic layer progress line: a short hex
// layer id followed only by a progress bar (e.g.
// "a1b2c3d4e5f6: [=====>     ] 1.2MB/5MB"). The whole-line shape check keeps
// it from ever eating a content line.
func isDockerHashProgress(t string) bool {
	id, rest, found := strings.Cut(t, ": ")
	if !found || !isDockerHexToken(id) {
		return false
	}
	return strings.Contains(rest, "[") && strings.Contains(rest, "]")
}

// isDockerHexToken reports whether s is a docker layer/digest id: an
// optional "sha256:" prefix followed by 8+ lowercase hex digits.
func isDockerHexToken(s string) bool {
	s = strings.TrimPrefix(s, "sha256:")
	if len(s) < 8 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Format compresses docker output; non-docker output falls back to generic.
func (d *Docker) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeDocker(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "docker: non-docker output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(d, raw, scrubbed, 0, "docker: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	var steps []string // step lines after the first (aggressive)
	firstStepKept := false
	dropped := 0

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if d.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isDockerLayerNoise(t) {
			dropped++
			continue
		}
		if level == LossAggressive && isDockerStep(t) {
			// Keep the first step inline; gather the rest to collapse into a
			// count once we know how many there are.
			if !firstStepKept {
				kept = append(kept, line)
				firstStepKept = true
				continue
			}
			steps = append(steps, line)
			continue
		}
		if t == "" {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	// Collapse the trailing step lines only when the summary is genuinely
	// smaller than the listing — otherwise keep the entries so Aggressive
	// never produces larger output than Balanced (mirrors the git
	// formatter's untracked-file collapse).
	if len(steps) > 0 {
		listing := strings.Join(steps, "\n")
		summary := fmt.Sprintf("  (+%d build steps)", len(steps))
		if len(summary) < len(listing) {
			kept = append(kept, summary)
			dropped += len(steps)
		} else {
			kept = append(kept, steps...)
		}
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("docker: %s, %d lines dropped", level, dropped)
	res := enforceCritical(d, raw, compact, dropped, notes)
	return res, true
}
