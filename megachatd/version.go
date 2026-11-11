// Package megachatd only carries the release version, embedded from the
// VERSION file next to go.mod: the single source of truth of the version.
package megachatd

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var rawVersion string

// Version is the contents of VERSION, trimmed (e.g. "0.2.1").
func Version() string { return strings.TrimSpace(rawVersion) }
