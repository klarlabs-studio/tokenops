package formatter

import (
	"fmt"
	"slices"
	"strings"
)

// DNF compresses the output of `dnf install` / `yum install` and related
// transactions. The transaction summary ("Transaction Summary" header and its
// "Install N Packages" line), the post-transaction "Installed:" summary and
// its package entries, the terminal "Complete!" line, and any error ("Error:"
// / "No match") are the signal an agent acts on and are always kept.
//
// The "Last metadata expiration check" preamble, the "Downloading Packages:"
// header with its per-package "(1/5): … kB/s" download lines, the "Running
// transaction check/test" chatter, and the aligned per-package transaction
// steps ("Installing : …", "Preparing : …", "Verifying : …") carry no state
// and are stripped at Balanced and above. At Aggressive the indented
// dependency-resolution table rows ("libfoo x86_64 1.0 repo size") collapse to
// a single count — the collapse uses the same size guard as the git
// formatter's untracked-file block, so it never produces output larger than
// the lines it replaces.
//
// Output that does not resemble dnf/yum is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type DNF struct{}

// NewDNF returns the dnf formatter.
func NewDNF() *DNF { return &DNF{} }

// Command reports the dnf command token.
func (d *DNF) Command() string { return "dnf" }

// Aliases registers the formatter under "yum" so legacy yum output routes here
// too. Command() remains the canonical token.
func (d *DNF) Aliases() []string { return []string{"yum"} }

// CriticalLine treats error and change-summary signal as critical: dnf error
// lines ("Error" / "No match" prefixes), the "Transaction Summary" header and
// its "Install N Packages" line, the post-transaction "Installed:" header and
// its package entries ("libfoo.x86_64 1.0"), and the terminal "Complete!"
// line. Metadata, download, and transaction-progress chatter are never
// critical.
func (d *DNF) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "Error"),
		strings.HasPrefix(t, "No match"),
		strings.HasPrefix(t, "Transaction Summary"),
		strings.HasPrefix(t, "Install ") && strings.Contains(t, "Package"),
		strings.HasPrefix(t, "Installed:"),
		t == "Complete!":
		return true
	}
	return isDNFInstalledEntry(t)
}

// dnfArches is the set of RPM architecture suffixes used to recognise
// "Installed:" package entries ("name.arch") without treating the
// resolution-table rows ("name arch …") as entries.
var dnfArches = []string{
	"x86_64", "noarch", "aarch64", "i686", "ppc64le", "s390x", "src", "armv7hl",
}

// isDNFArch reports whether s is an RPM architecture token.
func isDNFArch(s string) bool {
	return slices.Contains(dnfArches, s)
}

// isDNFInstalledEntry reports the post-transaction "Installed:" summary entries
// whose leading token carries a dotted architecture suffix ("libfoo.x86_64
// 1.0-1.el8"). These are critical: they name what the transaction actually
// changed. The dot-joined arch distinguishes them from the resolution-table
// rows, whose architecture is a separate whitespace-delimited field.
func isDNFInstalledEntry(t string) bool {
	fields := strings.Fields(t)
	if len(fields) < 2 {
		return false
	}
	dot := strings.LastIndex(fields[0], ".")
	if dot < 0 {
		return false
	}
	return isDNFArch(fields[0][dot+1:])
}

// looksLikeDNF reports whether b resembles dnf/yum output.
func looksLikeDNF(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Dependencies resolved") ||
		strings.Contains(s, "Transaction Summary") ||
		strings.Contains(s, "dnf") ||
		strings.Contains(s, "yum") ||
		strings.Contains(s, "Installed:") ||
		strings.Contains(s, "metadata expiration")
}

// isDNFNoise reports the preamble/download/transaction-progress lines that
// carry no state and are safe to drop at Balanced+. The transaction summary,
// the "Installed:" summary, and "Complete!" are handled by CriticalLine and
// are never dropped.
func isDNFNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "Last metadata expiration"),
		strings.HasPrefix(t, "Downloading Packages"),
		strings.HasPrefix(t, "Running transaction"),
		strings.HasPrefix(t, "Transaction test succeeded"),
		strings.HasPrefix(t, "Total download size"),
		strings.HasPrefix(t, "Installed size"):
		return true
	}
	return isDNFDownloadLine(t) || isDNFTxnProgress(t)
}

// isDNFDownloadLine reports the per-package download-progress lines emitted
// under "Downloading Packages:" ("(1/5): libfoo-1.0.x86_64.rpm  120 kB/s …").
func isDNFDownloadLine(t string) bool {
	return strings.Contains(t, "kB/s") ||
		strings.Contains(t, "MB/s") ||
		strings.Contains(t, " B/s")
}

// dnfTxnVerbs are the verbs that open an aligned per-package transaction step
// under "Running transaction".
var dnfTxnVerbs = []string{
	"Preparing", "Installing", "Upgrading", "Verifying",
	"Cleanup", "Removing", "Reinstalling", "Downgrading",
}

// isDNFTxnProgress matches the aligned per-package transaction steps emitted
// under "Running transaction": a leading verb, a run of spaces, a colon, and a
// progress "n/m" counter ("Installing : libfoo-… 1/5", "Preparing : 1/1",
// "Verifying : libbar-… 2/5"). Section headers ("Installing:", "Upgrading:")
// end with the colon and are deliberately excluded so they survive at Balanced.
func isDNFTxnProgress(t string) bool {
	if strings.HasSuffix(t, ":") {
		return false // section header, not a progress step
	}
	for _, v := range dnfTxnVerbs {
		if !strings.HasPrefix(t, v) {
			continue
		}
		rest := strings.TrimPrefix(t, v)
		if strings.HasPrefix(rest, " ") && strings.Contains(rest, ":") {
			return true
		}
	}
	return false
}

// isDNFTableRow reports an indented dependency-resolution table row
// ("libfoo x86_64 1.0-1.el8 baseos 120 k"): an indented line whose second
// whitespace-delimited field is an architecture token. These rows are kept in
// place at Balanced and collapsed to a count at Aggressive.
func isDNFTableRow(line string) bool {
	if !strings.HasPrefix(line, " ") {
		return false
	}
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "=") {
		return false
	}
	fields := strings.Fields(t)
	if len(fields) < 4 {
		return false
	}
	return isDNFArch(fields[1])
}

// Format compresses dnf/yum output; non-dnf output falls back to generic.
func (d *DNF) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeDNF(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "dnf: non-dnf output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(d, raw, scrubbed, 0, "dnf: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	var tableBody []string // resolution-table rows (Aggressive collapse)
	dropped := 0

	// flush collapses the collected resolution-table rows when the summary is
	// genuinely smaller than the listing — otherwise it keeps the entries so
	// the output never grows past the lines it replaces (mirrors the git
	// formatter's untracked-file collapse).
	flush := func() {
		if len(tableBody) == 0 {
			return
		}
		listing := strings.Join(tableBody, "\n")
		summary := fmt.Sprintf("  (+%d packages)", len(tableBody))
		if len(summary) < len(listing) {
			kept = append(kept, summary)
			dropped += len(tableBody)
		} else {
			kept = append(kept, tableBody...)
		}
		tableBody = nil
	}

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if d.CriticalLine(t) {
			flush()
			kept = append(kept, line)
			continue
		}
		// Only Aggressive collapses the resolution-table rows; at Balanced they
		// fall through and are kept in place.
		if level == LossAggressive && isDNFTableRow(line) {
			tableBody = append(tableBody, line)
			continue
		}
		flush()
		if isDNFNoise(t) || t == "" {
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	flush() // table body that ran to end of output

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("dnf: %s, %d lines dropped", level, dropped)
	res := enforceCritical(d, raw, compact, dropped, notes)
	return res, true
}
