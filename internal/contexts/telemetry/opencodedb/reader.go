package opencodedb

// Reader is the port the domains read opencode's store through: spend
// (vendorusage/opencode) and prompt and reply coaching take one rather than
// opening the database themselves. internal/infra/opencodedb.Store
// implements it, and the composition root (bootstrap, the CLI and the
// capabilities) wires it.
//
// Read visits every message in the store at path, honouring opts. A
// missing store is not an error: opencode may not be installed. A store
// with neither shape's tables is ErrSchema.
type Reader interface {
	Read(path string, opts Options, visit func(Message) error) error
}
