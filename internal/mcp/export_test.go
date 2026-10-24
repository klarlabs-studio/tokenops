package mcp

// Exported for the external test that parses shown commands with the
// terminal's own command tree (internal/cli imports this package, so only
// an external test package can import both).

// ShownCommand is the terminal command a call of a public tool would show
// the operator, and the route the call takes.
var ShownCommand = shownCommand

// ExactCases are the calls whose shown command must parse to exactly what
// the tool would apply.
var ExactCases = exactCases
