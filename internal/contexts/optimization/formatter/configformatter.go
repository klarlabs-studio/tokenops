package formatter

import (
	"fmt"
	"regexp"
	"strings"
)

// ConfigSpec is a user-supplied formatter declaration (from config). It lets
// an operator extend or override the catalog for a command WITHOUT
// recompiling tokenops: name the command, list the regexes that mark
// critical lines (always preserved), and list the noise regexes to drop at
// each loss level. The engine's critical-line guarantee still applies — a
// ConfigFormatter runs through the same enforceCritical guard as a built-in,
// so a mistaken drop rule can never remove a line the user marked critical.
type ConfigSpec struct {
	// Command is the token this formatter handles (e.g. "mytool").
	Command string
	// Aliases are extra command tokens routed to this formatter.
	Aliases []string
	// Critical lists regex sources; a line matching any is preserved
	// verbatim at every loss level.
	Critical []string
	// Drop maps a loss level to regex sources whose matching lines are
	// removed at that level and above. Rules under LossBalanced apply at
	// balanced AND aggressive; rules under LossAggressive apply only at
	// aggressive. LossConservative never drops (noise scrub only).
	Drop map[LossLevel][]string
}

// ConfigFormatter is a Formatter built from a user ConfigSpec.
type ConfigFormatter struct {
	command  string
	aliases  []string
	critical []*regexp.Regexp
	dropBal  []*regexp.Regexp // applied at balanced and aggressive
	dropAgg  []*regexp.Regexp // applied at aggressive only
}

// NewConfigFormatter compiles spec into a ConfigFormatter. It returns an
// error when the command is empty or any regex fails to compile, so bad
// config is rejected at load time rather than silently ignored at runtime.
func NewConfigFormatter(spec ConfigSpec) (*ConfigFormatter, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return nil, fmt.Errorf("config formatter: command must not be empty")
	}
	crit, err := compileAll(spec.Command, "critical", spec.Critical)
	if err != nil {
		return nil, err
	}
	bal, err := compileAll(spec.Command, "drop.balanced", spec.Drop[LossBalanced])
	if err != nil {
		return nil, err
	}
	agg, err := compileAll(spec.Command, "drop.aggressive", spec.Drop[LossAggressive])
	if err != nil {
		return nil, err
	}
	return &ConfigFormatter{
		command:  spec.Command,
		aliases:  append([]string(nil), spec.Aliases...),
		critical: crit,
		dropBal:  bal,
		dropAgg:  agg,
	}, nil
}

func compileAll(command, field string, sources []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(sources))
	for _, src := range sources {
		re, err := regexp.Compile(src)
		if err != nil {
			return nil, fmt.Errorf("config formatter %q: %s: invalid regex %q: %w", command, field, src, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// Command reports the command token.
func (c *ConfigFormatter) Command() string { return c.command }

// Aliases reports extra command tokens (satisfies the registry aliaser).
func (c *ConfigFormatter) Aliases() []string { return c.aliases }

// sniffExclude keeps ConfigFormatters out of the proxy content-sniff plane:
// their rules are user-scoped and carry no reliable content signature, so
// they must only run when dispatched by their explicit command token.
func (c *ConfigFormatter) sniffExclude() bool { return true }

// CriticalLine reports whether line matches any configured critical regex.
func (c *ConfigFormatter) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	for _, re := range c.critical {
		if re.MatchString(t) {
			return true
		}
	}
	return false
}

// Format applies the noise scrub, then drops lines matching the loss-level
// drop rules while preserving every critical line, ending with the shared
// enforceCritical guard.
func (c *ConfigFormatter) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if level == LossConservative {
		res := enforceCritical(c, raw, scrubbed, 0, "config: conservative scrub")
		return res, true
	}

	drops := c.dropBal
	if level == LossAggressive {
		drops = append(append([]*regexp.Regexp(nil), c.dropBal...), c.dropAgg...)
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if c.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if matchesAny(t, drops) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("config[%s]: %s, %d lines dropped", c.command, level, dropped)
	res := enforceCritical(c, raw, compact, dropped, notes)
	return res, true
}

func matchesAny(s string, res []*regexp.Regexp) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
