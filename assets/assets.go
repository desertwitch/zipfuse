// Package assets provides the assets for the ZipFUSE program.
package assets

import _ "embed"

// Logo is a byte slice containing the program logo.
//
//go:embed zipfuse.png
var Logo []byte
