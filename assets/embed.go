// Package assets provides embedded assets for the zipfuse program.
package assets

import _ "embed"

// Logo is a byte slice containing the embedded zipfuse program logo.
//
//go:embed zipfuse.png
var Logo []byte
