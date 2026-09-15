// Package share embeds the static viewer for encrypted share pages.
package share

import _ "embed"

// Viewer is the HTML template; {{CIPHERTEXT}} is replaced by the armored
// age ciphertext of the snapshot.
//
//go:embed viewer.html
var Viewer string
