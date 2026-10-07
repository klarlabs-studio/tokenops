package spending

import "go.klarlabs.de/tokenops/internal/contexts/spend/spend"

// Engine prices events at the rate card in force when they ran. The
// daemon, the CLI and the MCP server hand one around as a dependency;
// bootstrap.SpendEngine builds it.
type Engine = spend.Engine

// Table is one rate card: each model's price per million tokens.
type Table = spend.Table

// DefaultTable is the rate card compiled into the binary.
func DefaultTable() Table { return spend.DefaultTable() }

// NewEngine prices with t alone, for a caller with no snapshots to read.
func NewEngine(t Table) *Engine { return spend.NewEngine(t) }
