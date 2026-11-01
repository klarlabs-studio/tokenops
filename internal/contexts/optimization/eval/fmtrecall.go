package eval

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter"
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer"
)

// The fmt recall evaluation judges command-output compression by what an
// agent can still find in the compacted text, not by how much was removed.
// Each case is a captured command output with the evidence an agent needs
// from it labeled by hand: failing test names, file:line of errors, error
// messages, changed paths, exit status, counts. Recall is the share of
// labeled items still recoverable from the formatted output.
//
// The labels are independent of the formatters' own CriticalLine
// predicates, so the evaluation catches what the engine's enforceCritical
// guard cannot: a formatter that never classified a line as critical in
// the first place.

// EvidenceKind names what a labeled evidence item is.
type EvidenceKind string

// The evidence kinds. The first six are critical: the categories the
// engine's critical-line guarantee exists to protect (errors, failures,
// changed state). Counts, matches and installed packages are measured but
// may be summarized: a package manager's install list is in the lockfile,
// and its formatter collapses it to a count by design.
const (
	EvidenceFailingTest    EvidenceKind = "failing_test"
	EvidenceErrorLocation  EvidenceKind = "error_location"
	EvidenceErrorMessage   EvidenceKind = "error_message"
	EvidenceChangedPath    EvidenceKind = "changed_path"
	EvidenceExitStatus     EvidenceKind = "exit_status"
	EvidenceFailedResource EvidenceKind = "failed_resource"
	EvidenceCount          EvidenceKind = "count"
	EvidenceMatch          EvidenceKind = "match"
	EvidenceInstalled      EvidenceKind = "installed"
)

// criticalKinds are the kinds critical unless an item says otherwise.
var criticalKinds = map[EvidenceKind]bool{
	EvidenceFailingTest: true, EvidenceErrorLocation: true, EvidenceErrorMessage: true,
	EvidenceChangedPath: true, EvidenceExitStatus: true, EvidenceFailedResource: true,
	EvidenceCount: false, EvidenceMatch: false, EvidenceInstalled: false,
}

// GenericFormatter is the name a case is grouped under when no
// command-specific formatter handles its command.
const GenericFormatter = "generic"

// Evidence is one labeled item an agent needs from a command's output.
type Evidence struct {
	Kind EvidenceKind `json:"kind"`
	Text string       `json:"text"`
	// Critical overrides the kind's default criticality when set.
	Critical *bool `json:"critical,omitempty"`
}

// IsCritical reports whether losing the item breaks the agent's task.
func (e Evidence) IsCritical() bool {
	if e.Critical != nil {
		return *e.Critical
	}
	return criticalKinds[e.Kind]
}

// RecallCase is one labeled command output.
type RecallCase struct {
	ID          string     `json:"id"`
	Command     string     `json:"command"`
	Description string     `json:"description,omitempty"`
	Output      string     `json:"-"`
	Evidence    []Evidence `json:"evidence"`
}

// MatchKind is how an evidence item was found in the compact output.
type MatchKind string

// The match kinds: verbatim, after collapsing whitespace, or not at all.
const (
	MatchExact      MatchKind = "exact"
	MatchNormalized MatchKind = "normalized"
	MatchNone       MatchKind = "missed"
)

// ItemResult is one evidence item's outcome.
type ItemResult struct {
	Kind     EvidenceKind `json:"kind"`
	Text     string       `json:"text"`
	Critical bool         `json:"critical"`
	Recalled bool         `json:"recalled"`
	Match    MatchKind    `json:"match"`
	// Guarded reports that the raw line holding the item is one the
	// formatter classifies as critical, so enforceCritical protects it.
	Guarded bool `json:"guarded"`
}

// CaseResult is one case at one loss level.
type CaseResult struct {
	ID        string       `json:"id"`
	Command   string       `json:"command"`
	Formatter string       `json:"formatter"`
	Level     string       `json:"level"`
	Items     []ItemResult `json:"items"`
	Tally
	// FellBack reports that the formatter refused to compress (the
	// critical-line guard tripped) and forwarded the raw output.
	FellBack bool `json:"fell_back"`
}

// Tally sums recall and savings over a set of cases.
type Tally struct {
	Cases            int `json:"cases"`
	Items            int `json:"items"`
	Recalled         int `json:"recalled"`
	CriticalItems    int `json:"critical_items"`
	CriticalRecalled int `json:"critical_recalled"`
	BytesBefore      int `json:"bytes_before"`
	BytesAfter       int `json:"bytes_after"`
	TokensBefore     int `json:"tokens_before"`
	TokensAfter      int `json:"tokens_after"`
	FellBack         int `json:"fell_back"`
}

// Recall is the share of labeled items recovered; 1 when none were labeled.
func (t Tally) Recall() float64 { return ratio(t.Recalled, t.Items) }

// CriticalRecall is the share of critical items recovered.
func (t Tally) CriticalRecall() float64 { return ratio(t.CriticalRecalled, t.CriticalItems) }

// SavedPct is the byte reduction as a percentage of the raw output.
func (t Tally) SavedPct() float64 {
	if t.BytesBefore == 0 || t.BytesAfter >= t.BytesBefore {
		return 0
	}
	return 100 * float64(t.BytesBefore-t.BytesAfter) / float64(t.BytesBefore)
}

// SavedTokens is the estimated token reduction, never negative.
func (t Tally) SavedTokens() int { return max(t.TokensBefore-t.TokensAfter, 0) }

func (t *Tally) add(o Tally) {
	t.Cases += o.Cases
	t.Items += o.Items
	t.Recalled += o.Recalled
	t.CriticalItems += o.CriticalItems
	t.CriticalRecalled += o.CriticalRecalled
	t.BytesBefore += o.BytesBefore
	t.BytesAfter += o.BytesAfter
	t.TokensBefore += o.TokensBefore
	t.TokensAfter += o.TokensAfter
	t.FellBack += o.FellBack
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 1
	}
	return float64(n) / float64(d)
}

// FormatterRecall is one formatter's tally at one level.
type FormatterRecall struct {
	Formatter string `json:"formatter"`
	Tally
}

// LevelRecall is the evaluation at one loss level.
type LevelRecall struct {
	Level      string            `json:"level"`
	Overall    Tally             `json:"overall"`
	Formatters []FormatterRecall `json:"formatters"`
	Cases      []CaseResult      `json:"cases"`
	// Misses lists every item not recovered, critical first.
	Misses []Miss `json:"misses"`
}

// Miss is one evidence item the compact output lost.
type Miss struct {
	Case      string       `json:"case"`
	Formatter string       `json:"formatter"`
	Kind      EvidenceKind `json:"kind"`
	Text      string       `json:"text"`
	Critical  bool         `json:"critical"`
}

// RecallEvaluation is the evaluation at every requested level.
type RecallEvaluation struct {
	Levels []LevelRecall `json:"levels"`
}

// CriticalMisses returns every critical item lost at any level.
func (e RecallEvaluation) CriticalMisses() []Miss {
	var out []Miss
	for _, lv := range e.Levels {
		for _, m := range lv.Misses {
			if m.Critical {
				out = append(out, m)
			}
		}
	}
	return out
}

// AllLevels are the loss levels in increasing order of loss.
var AllLevels = []formatter.LossLevel{formatter.LossConservative, formatter.LossBalanced, formatter.LossAggressive}

// EvaluateFmtRecall runs every case through formatters at each level and
// scores evidence recall and savings. A command no formatter handles goes
// through the generic scrub, as it would in production.
func EvaluateFmtRecall(cases []RecallCase, formatters []formatter.Formatter, levels []formatter.LossLevel) RecallEvaluation {
	counter := tokenizer.NewAnthropicTokenizer()
	ev := RecallEvaluation{Levels: make([]LevelRecall, 0, len(levels))}
	for _, level := range levels {
		reg := formatter.NewRegistry(formatter.LossPolicy{Default: level}, formatters...)
		lv := LevelRecall{Level: level.String()}
		by := map[string]*FormatterRecall{}
		for _, c := range cases {
			cr := evaluateCase(reg, c, level, counter)
			lv.Cases = append(lv.Cases, cr)
			lv.Overall.add(cr.Tally)
			fr := by[cr.Formatter]
			if fr == nil {
				fr = &FormatterRecall{Formatter: cr.Formatter}
				by[cr.Formatter] = fr
			}
			fr.add(cr.Tally)
			for _, it := range cr.Items {
				if !it.Recalled {
					lv.Misses = append(lv.Misses, Miss{Case: cr.ID, Formatter: cr.Formatter, Kind: it.Kind, Text: it.Text, Critical: it.Critical})
				}
			}
		}
		for _, fr := range by {
			lv.Formatters = append(lv.Formatters, *fr)
		}
		sort.Slice(lv.Formatters, func(i, j int) bool { return lv.Formatters[i].Formatter < lv.Formatters[j].Formatter })
		sort.SliceStable(lv.Misses, func(i, j int) bool { return lv.Misses[i].Critical && !lv.Misses[j].Critical })
		ev.Levels = append(ev.Levels, lv)
	}
	return ev
}

func evaluateCase(reg *formatter.Registry, c RecallCase, level formatter.LossLevel, counter tokenizer.Tokenizer) CaseResult {
	raw := []byte(c.Output)
	res, _ := reg.Format([]string{c.Command}, raw)
	name := GenericFormatter
	f := reg.Formatter(c.Command)
	if f != nil {
		name = f.Command()
	}
	compact := string(res.Compact)
	normCompact := normalizeEvidence(compact)
	cr := CaseResult{ID: c.ID, Command: c.Command, Formatter: name, Level: level.String()}
	cr.Cases = 1
	cr.BytesBefore, cr.BytesAfter = len(raw), len(res.Compact)
	cr.TokensBefore, cr.TokensAfter = counter.CountText(c.Output), counter.CountText(compact)
	if !res.CriticalKept {
		cr.FellBack, cr.Tally.FellBack = true, 1
	}
	for _, e := range c.Evidence {
		it := ItemResult{Kind: e.Kind, Text: e.Text, Critical: e.IsCritical(), Match: MatchNone, Guarded: f != nil && guarded(f, c.Output, e.Text)}
		switch {
		case strings.Contains(compact, e.Text):
			it.Recalled, it.Match = true, MatchExact
		case strings.Contains(normCompact, normalizeEvidence(e.Text)):
			it.Recalled, it.Match = true, MatchNormalized
		}
		cr.Items = append(cr.Items, it)
		cr.tallyItem(it)
	}
	return cr
}

// tallyItem folds one item into the case's tally.
func (c *CaseResult) tallyItem(it ItemResult) {
	c.Tally.Items++
	if it.Recalled {
		c.Recalled++
	}
	if it.Critical {
		c.CriticalItems++
		if it.Recalled {
			c.CriticalRecalled++
		}
	}
}

// guarded reports whether some raw line holding text is a critical line.
func guarded(f formatter.Formatter, raw, text string) bool {
	want := normalizeEvidence(text)
	for line := range strings.SplitSeq(raw, "\n") {
		t := strings.TrimRight(line, " \t\r")
		if strings.Contains(normalizeEvidence(t), want) && f.CriticalLine(t) {
			return true
		}
	}
	return false
}

var (
	ansiSeq    = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	whitespace = regexp.MustCompile(`\s+`)
)

// normalizeEvidence strips ANSI escapes and collapses whitespace runs, so
// re-indented or re-aligned output still counts as recovered.
func normalizeEvidence(s string) string {
	return strings.TrimSpace(whitespace.ReplaceAllString(ansiSeq.ReplaceAllString(s, ""), " "))
}

// recallCorpus holds the labeled command outputs, compiled into the binary.
//
//go:embed testdata/fmtrecall
var recallCorpus embed.FS

// RecallCorpusDir is where the bundled labeled corpus lives in the source
// tree, relative to this package.
const RecallCorpusDir = "testdata/fmtrecall"

// BundledRecallCases returns the labeled corpus shipped with TokenOps.
func BundledRecallCases() ([]RecallCase, error) {
	return LoadRecallCases(recallCorpus, RecallCorpusDir)
}

// recallLabel is a case's sidecar file: <id>.json beside <id>.txt.
type recallLabel struct {
	Command     string     `json:"command"`
	Description string     `json:"description"`
	Evidence    []Evidence `json:"evidence"`
}

// LoadRecallCases reads every <id>.json label in dir with its <id>.txt
// output. The id's first dot-separated token is the command unless the
// label names one. A label whose evidence is absent from its own output is
// rejected: the corpus would otherwise score a typo as a formatter bug.
func LoadRecallCases(fsys fs.FS, dir string) ([]RecallCase, error) {
	names, err := fs.Glob(fsys, path.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("eval: list recall corpus: %w", err)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("eval: no labeled outputs (<id>.json with <id>.txt) in %s", dir)
	}
	sort.Strings(names)
	cases := make([]RecallCase, 0, len(names))
	for _, name := range names {
		c, err := loadRecallCase(fsys, name)
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	return cases, nil
}

func loadRecallCase(fsys fs.FS, labelPath string) (RecallCase, error) {
	id := strings.TrimSuffix(path.Base(labelPath), ".json")
	data, err := fs.ReadFile(fsys, labelPath)
	if err != nil {
		return RecallCase{}, fmt.Errorf("eval: read %s: %w", labelPath, err)
	}
	var lbl recallLabel
	if err := json.Unmarshal(data, &lbl); err != nil {
		return RecallCase{}, fmt.Errorf("eval: parse %s: %w", labelPath, err)
	}
	outPath := strings.TrimSuffix(labelPath, ".json") + ".txt"
	out, err := fs.ReadFile(fsys, outPath)
	if err != nil {
		return RecallCase{}, fmt.Errorf("eval: %s has no output %s: %w", labelPath, path.Base(outPath), err)
	}
	if len(lbl.Evidence) == 0 {
		return RecallCase{}, fmt.Errorf("eval: %s labels no evidence", labelPath)
	}
	command := lbl.Command
	if command == "" {
		command = strings.SplitN(id, ".", 2)[0]
	}
	normOut := normalizeEvidence(string(out))
	for _, e := range lbl.Evidence {
		if _, ok := criticalKinds[e.Kind]; !ok {
			return RecallCase{}, fmt.Errorf("eval: %s: unknown kind %q", labelPath, e.Kind)
		}
		if e.Text == "" || !strings.Contains(normOut, normalizeEvidence(e.Text)) {
			return RecallCase{}, fmt.Errorf("eval: %s: evidence %q is not in the raw output", labelPath, e.Text)
		}
	}
	return RecallCase{ID: id, Command: command, Description: lbl.Description, Output: string(out), Evidence: lbl.Evidence}, nil
}
