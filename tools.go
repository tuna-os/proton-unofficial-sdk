//go:build tools

// The gomobile bindings import these packages; requiring them here pins
// golang.org/x/mobile in go.mod (and vendor/) at the version the build
// image's gomobile and gobind come from.
package tools

import (
	_ "golang.org/x/mobile/bind"
	_ "golang.org/x/mobile/bind/java"
	_ "golang.org/x/mobile/bind/seq"
)
