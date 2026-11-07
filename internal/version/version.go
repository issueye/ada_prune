// Package version holds the build identity injected at link time.
package version

// Version is the application version, overridden in release builds through
//
//	-ldflags "-X github.com/learningdog1/ada_prune/internal/version.Version=x.y.z"
var Version = "0.1.0"

// Commit is the source revision, "dev" outside release builds.
var Commit = "dev"

// String returns the version, suffixed with the commit when it is known.
func String() string {
	if Commit == "" || Commit == "dev" {
		return Version
	}
	return Version + "+" + Commit
}
