// Package version exposes build-time metadata about the TokenOps binary.
package version

import "runtime/debug"

// Build metadata, populated via -ldflags at build time. A binary built
// without them (go install …@latest, go build) takes them from what the
// Go toolchain recorded in it instead; see init.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		Version, Commit, Date = fromBuildInfo(info, Version, Commit, Date)
	}
}

// fromBuildInfo fills each field the linker left at its default from the
// build info: the module version go install recorded (v0.118.18), and the
// revision and commit time a build from a checkout recorded. A field the
// linker set is kept; a build from a checkout reports "(devel)" as its
// module version, which says nothing, so the version stays "dev".
func fromBuildInfo(info *debug.BuildInfo, version, commit, date string) (string, string, string) {
	if version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision" && commit == "unknown" && s.Value != "":
			commit = s.Value
			if len(commit) > 12 {
				commit = commit[:12]
			}
		case s.Key == "vcs.time" && date == "unknown" && s.Value != "":
			date = s.Value
		}
	}
	return version, commit, date
}

// String returns a human-readable build identifier.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}
