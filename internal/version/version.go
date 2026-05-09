// Package version exposes build-time metadata about the TokenOps binary.
package version

// Build metadata, populated via -ldflags at build time.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String returns a human-readable build identifier.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}
