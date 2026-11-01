package fmtinsight

import (
	"os"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/eval"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter"
)

// Invariant is what fmt never drops, stated once for every surface that
// reports the recall evaluation.
const Invariant = "Every line a formatter marks critical (errors, failures, changed state) " +
	"survives verbatim at every loss level; a formatter that would drop one forwards the raw output instead."

// Recall evaluation types, re-exported for the adapters.
type (
	// LevelRecall is the evaluation at one loss level.
	LevelRecall = eval.LevelRecall
	// FormatterRecall is one formatter's recall and savings at one level.
	FormatterRecall = eval.FormatterRecall
	// RecallMiss is one labeled evidence item the compact output lost.
	RecallMiss = eval.Miss
)

// RecallReport is the fmt evaluation: the invariant, then evidence recall
// and savings per loss level over a labeled corpus.
type RecallReport struct {
	Invariant string `json:"invariant"`
	// Corpus names where the labeled outputs came from: "bundled" or a
	// directory.
	Corpus string `json:"corpus"`
	// Formatters is the number of built-in formatters judged.
	Formatters int           `json:"formatters"`
	Levels     []LevelRecall `json:"levels"`
	// CriticalMisses lists every critical item lost at any level; empty
	// when the invariant held over the whole corpus.
	CriticalMisses []RecallMiss `json:"critical_misses"`
}

// Recall runs the built-in formatters over a labeled corpus at every loss
// level. An empty dir evaluates the corpus bundled into the binary; a
// directory holds <id>.txt outputs with <id>.json labels. The operator's
// config formatters are left out: the report describes this build.
func Recall(dir string) (RecallReport, error) {
	var (
		cases []eval.RecallCase
		err   error
	)
	corpus := "bundled"
	if dir == "" {
		cases, err = eval.BundledRecallCases()
	} else {
		corpus = dir
		cases, err = eval.LoadRecallCases(os.DirFS(dir), ".")
	}
	if err != nil {
		return RecallReport{}, err
	}
	formatters := formatter.DefaultFormatters()
	ev := eval.EvaluateFmtRecall(cases, formatters, eval.AllLevels)
	return RecallReport{
		Invariant:      Invariant,
		Corpus:         corpus,
		Formatters:     len(formatters),
		Levels:         ev.Levels,
		CriticalMisses: append([]RecallMiss{}, ev.CriticalMisses()...),
	}, nil
}
