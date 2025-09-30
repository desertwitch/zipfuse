// Package assets provides the assets for the zipfuse program.
package assets

import _ "embed"

// Logo is a byte slice containing the program logo as PNG.
//
//go:embed zipfuse.png
var Logo []byte
