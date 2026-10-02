// Package buildinfo exposes version/build metadata, injected at build time via
// -ldflags and surfaced on the service metadata endpoint and in logs.
package buildinfo

// These are overridden at build time, e.g.:
//
//	-ldflags "-X github.com/dibakshya01/purple-sparrow/internal/buildinfo.Version=v0.1.0"
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Name is the product/service name.
const Name = "purple-sparrow"

// Info is the serializable build metadata.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// Get returns the current build info.
func Get() Info {
	return Info{Name: Name, Version: Version, Commit: Commit, Date: Date}
}
