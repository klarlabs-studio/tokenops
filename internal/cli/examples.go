package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// examples lead each command's help, as clig.dev asks: a person usually
// wants to see the command run before reading what it does. A test parses
// every line against the real command tree, so an example cannot drift
// from the flags it shows.
var examples = map[string]string{
	"story": `tokenops story                   # the last week, task by task
tokenops story --days 1 --json`,
	"explain": `tokenops explain                 # every term
tokenops explain wall-clock
tokenops explain decision:6f1c…  # why TokenOps decided what it did`,
	"init": `tokenops init                    # set up this machine
tokenops init --yes              # take every default`,
	"detect":     `tokenops detect`,
	"statusline": `tokenops statusline install      # Claude Code runs it on every turn`,
	"plan set": `tokenops plan set anthropic claude-max-20x
tokenops plan set openai gpt-pro-5x --price 229 --currency EUR`,
	"provider set":        `tokenops provider set openai https://api.openai.com`,
	"vendor-usage enable": `tokenops vendor-usage enable codex-jsonl`,
	"coach preset":        `tokenops coach preset advise`,
	"coach autonomy":      `tokenops coach autonomy ask`,
	"coach delivery":      `tokenops coach delivery advise`,
	"routing rule set":    `tokenops routing rule set anthropic claude-opus-5 claude-sonnet-5 --quality 0.9`,
	"fmt": `tokenops fmt -- go test ./...
tokenops fmt --level aggressive -- git log`,
	"fmt bench":   `tokenops fmt bench --corpus ./captured`,
	"fmt hook":    `tokenops fmt hook --shell zsh`,
	"fmt recover": `tokenops fmt recover 01J9Z…`,
	"fmt learn":   `tokenops fmt learn --apply`,
	"fmt analyze": `tokenops fmt analyze --top 10`,
	"glance": `tokenops glance                  # every plan as a card, with the coach's findings
tokenops glance --brief          # a table instead
tokenops glance --findings       # only the findings, all of them`,
	"spend": `tokenops spend                   # the last 7 days
tokenops spend --since 24h --by provider
tokenops spend --forecast --json
tokenops spend --by commit --since 30d   # what each of your commits cost`,
	"dx": `tokenops dx                      # the last week, from the daemon's analysis
tokenops dx --fresh --days 30`,
	"status": `tokenops status
tokenops status --json`,
	"daemon install": `tokenops daemon install
tokenops daemon install --dry-run   # print the unit, write nothing`,
	"daemon uninstall": `tokenops daemon uninstall`,
	"daemon status":    `tokenops daemon status --json`,
	"daemon restart":   `tokenops daemon restart`,
	"hooks install": `tokenops hooks install --coach --read-guard
tokenops hooks install --client codex --coach`,
	"hooks uninstall":      `tokenops hooks uninstall --read-guard`,
	"hooks status":         `tokenops hooks status --client opencode --json`,
	"statusline install":   `tokenops statusline install`,
	"statusline uninstall": `tokenops statusline uninstall`,
	"statusline subagents": `tokenops statusline subagents`,
	"menubar":              `tokenops menubar`,
	"plan list":            `tokenops plan list --json`,
	"plan headroom":        `tokenops plan headroom`,
	"plan catalog":         `tokenops plan catalog`,
	"plan unset":           `tokenops plan unset anthropic`,
	"plan history":         `tokenops plan history --json`,
	"budget list":          `tokenops budget list`,
	"budget set": `tokenops budget set monthly --limit-usd 200
tokenops budget set weekly-tokens --basis tokens --limit-tokens 50000000 --window weekly`,
	"budget unset":          `tokenops budget unset monthly`,
	"provider list":         `tokenops provider list`,
	"provider unset":        `tokenops provider unset openai`,
	"vendor-usage status":   `tokenops vendor-usage status --json`,
	"vendor-usage backfill": `tokenops vendor-usage backfill --hours 72 --dry-run`,
	"vendor-usage setup": `tokenops vendor-usage setup claude-subscription
tokenops vendor-usage setup claude-subscription --paste`,
	"pricing refresh": `tokenops pricing refresh --dry-run`,
	"pricing show":    `tokenops pricing show --json`,
	"pricing diff":    `tokenops pricing diff --from baseline --to latest`,
	"pricing lint":    `tokenops pricing lint`,
	"config show":     `tokenops config show`,
	"otel": `tokenops otel                    # every figure the daemon pushes, computed now
tokenops otel --json`,
	"version": `tokenops version --json`,
	"coach": `tokenops coach                   # the dials, what the coach did, what came of it
tokenops coach preset advise`,
	"coach stats":     `tokenops coach stats --json`,
	"coach verbosity": `tokenops coach verbosity quiet`,
	"coach set":       `tokenops coach set waste autonomous`,
	"coach off":       `tokenops coach off`,
	"coach migrate":   `tokenops coach migrate`,
	"coach prompts":   `tokenops coach prompts --since 7d`,
	"coach replies":   `tokenops coach replies --since 24h --json`,
	"mode": `tokenops mode                    # print the mode
tokenops mode active`,
	"routing proposals":     `tokenops routing proposals --json`,
	"routing rule list":     `tokenops routing rule list`,
	"routing rule unset":    `tokenops routing rule unset anthropic claude-opus-5`,
	"preferred-model list":  `tokenops preferred-model list`,
	"preferred-model set":   `tokenops preferred-model set anthropic claude-sonnet-5`,
	"preferred-model unset": `tokenops preferred-model unset anthropic`,
	"experiment start": `tokenops experiment start anthropic claude-opus-5 claude-sonnet-5 \
  --objective tokens --min-improvement-pct 10 --guardrail quality`,
	"experiment status":  `tokenops experiment status experiment:6f1c…`,
	"experiment stop":    `tokenops experiment stop experiment:6f1c…`,
	"rules analyze":      `tokenops rules analyze --root .`,
	"rules conflicts":    `tokenops rules conflicts --root .`,
	"rules compress":     `tokenops rules compress --root . --json`,
	"rules inject":       `tokenops rules inject --root . --file internal/cli/root.go --token-budget 2000`,
	"rules bench":        `tokenops rules bench --spec bench.yaml`,
	"task start":         `tokenops task start "price the Codex models"`,
	"task done":          `tokenops task done`,
	"task list":          `tokenops task list --since 7d --metrics`,
	"outcome record":     `tokenops outcome record exec-42 --result achieved`,
	"outcome detect":     `tokenops outcome detect exec-42 --session-id 9420028c`,
	"outcome check-json": `tokenops outcome check-json exec-42 --file response.json --pointer /status --equals ok`,
	"scorecard":          `tokenops scorecard --days 30`,
	"optimizations":      `tokenops optimizations --since 24h --json`,
	"verify":             `tokenops verify --days 30 --each`,
	"audit":              `tokenops audit --since 7d --action config_change`,
	"events":             `tokenops events --since 24h`,
}

// applyExamples sets each command's example from examples.
func applyExamples(root *cobra.Command) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			path := strings.TrimPrefix(s.CommandPath(), root.Name()+" ")
			if ex, ok := examples[path]; ok && s.Example == "" {
				s.Example = indent(ex)
			}
			walk(s)
		}
	}
	walk(root)
}

// indent sets each example line in by two spaces, as cobra prints them.
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}
