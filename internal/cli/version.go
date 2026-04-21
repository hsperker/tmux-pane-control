package cli

import (
	"fmt"
	"runtime/debug"
)

// versionString returns a one-line version identifier for the
// running tpctl binary. It uses runtime/debug.ReadBuildInfo so the
// value comes from whatever metadata the Go toolchain embedded at
// build time:
//
//   - `go install ...@vX.Y.Z` → BuildInfo.Main.Version = "vX.Y.Z".
//   - `go build` from a git worktree → Main.Version is "(devel)"
//     and BuildInfo.Settings carries vcs.revision / vcs.time.
//   - A raw `go build` outside a module → minimal info.
//
// Callers should treat the return value as opaque and only display
// it; the format is best-effort, not a machine contract.
func versionString() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "tpctl (version information unavailable)"
	}

	version := info.Main.Version
	if version == "" || version == "(devel)" {
		// Enrich the devel case with commit + time if available.
		var rev, tim, dirty string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.time":
				tim = s.Value
			case "vcs.modified":
				if s.Value == "true" {
					dirty = "+dirty"
				}
			}
		}
		if rev != "" {
			short := rev
			if len(short) > 12 {
				short = short[:12]
			}
			if tim != "" {
				return fmt.Sprintf("tpctl devel %s%s (%s)", short, dirty, tim)
			}
			return fmt.Sprintf("tpctl devel %s%s", short, dirty)
		}
		return "tpctl devel"
	}

	return fmt.Sprintf("tpctl %s", version)
}
