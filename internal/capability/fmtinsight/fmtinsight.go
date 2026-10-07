// Package fmtinsight answers what `tokenops fmt` would do with the
// operator's real traffic: what fills the context and what each command's
// output would compress to (analyze), and where the formatter catalog
// should improve (learn).
//
// Both read the formatter set the operator runs: the built-in catalog
// with the formatters their config adds. The MCP tools used the built-in
// catalog alone, so a command the operator had written a formatter for
// was reported unhandled and proposed as the next formatter to write.
package fmtinsight

import (
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/fmtlearn"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter"
	"go.klarlabs.de/tokenops/internal/infra/fmtindex"
	"go.klarlabs.de/tokenops/internal/infra/jsonlfmt"
)

// Formatter compresses one command's output.
type Formatter = formatter.Formatter

// Analysis is what fills the context and what fmt would save on it.
type Analysis = jsonlfmt.Report

// LearnReport is where the formatter catalog should improve.
type LearnReport = fmtlearn.Report

// Formatters is the built-in catalog with cfg's formatters added; a
// configured command replaces the built-in one. Warnings name the
// configured formatters that could not be built and were left out.
func Formatters(cfg config.CommandFmtConfig) ([]Formatter, []string) {
	out := formatter.DefaultFormatters()
	var warns []string
	for _, fc := range cfg.Formatters {
		f, err := formatter.NewConfigFormatter(formatter.ConfigSpec{
			Command:  fc.Command,
			Aliases:  fc.Aliases,
			Critical: fc.Critical,
			Drop: map[formatter.LossLevel][]string{
				formatter.LossBalanced:   fc.Drop.Balanced,
				formatter.LossAggressive: fc.Drop.Aggressive,
			},
		})
		if err != nil {
			warns = append(warns, err.Error())
			continue
		}
		out = append(out, f)
	}
	return out, warns
}

// Window selects the Claude Code sessions to read.
type Window struct {
	// Root is the projects directory; empty is ~/.claude/projects.
	Root string
	// MaxFiles caps the sessions read, newest first; 0 reads them all.
	MaxFiles int
}

// Analyze dry-runs every Bash command's output in w through the
// formatters cfg configures.
func Analyze(cfg config.CommandFmtConfig, w Window, now time.Time) (*Analysis, error) {
	formatters, _ := Formatters(cfg)
	rep, _, err := jsonlfmt.Scan(formatters, jsonlfmt.Options{Root: w.Root, MaxFiles: w.MaxFiles}, now)
	return rep, err
}

// LearnOptions selects what learn reads.
type LearnOptions struct {
	// RecoverDir is the recovery index of wrapped runs; empty is the
	// default ~/.tokenops/recovery.
	RecoverDir string
	// Sessions, when set, folds in Claude Code sessions so the report
	// reflects real usage without any wrapped runs.
	Sessions *Window
}

// Learn reads the wrapped-run index, and the sessions o names, and says
// where the catalog should improve. A session scan that fails leaves the
// index's signal alone rather than failing.
func Learn(cfg config.CommandFmtConfig, o LearnOptions, now time.Time) (LearnReport, error) {
	recs, err := fmtindex.Read(o.RecoverDir)
	if err != nil {
		return LearnReport{}, err
	}
	if o.Sessions != nil {
		formatters, _ := Formatters(cfg)
		if _, jrecs, err := jsonlfmt.Scan(formatters, jsonlfmt.Options{Root: o.Sessions.Root, MaxFiles: o.Sessions.MaxFiles}, now); err == nil {
			recs = append(recs, jrecs...)
		}
	}
	return fmtlearn.Analyze(recs, fmtlearn.Thresholds{}), nil
}
