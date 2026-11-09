// Package version holds build information injected at link time.
package version

import "runtime"

// These values are overridden with -ldflags "-X ..." by the build.
var (
	// Version is the semantic version, e.g. "0.1.0" or "0.1.0-test.12".
	Version = "0.0.0-dev"
	// Commit is the git commit the binary was built from.
	Commit = "unknown"
	// Channel is the update channel: "production" or "test".
	Channel = "test"
	// BuildDate is the UTC build time in RFC 3339 format.
	BuildDate = "unknown"
)

// Info describes the running build.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Channel   string `json:"channel"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// Get returns the build information of the running binary.
func Get() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		Channel:   Channel,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}
