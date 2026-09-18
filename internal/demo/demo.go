// Package demo embeds the walkthrough script so `secretree demo` can run
// it without a checkout of the repository or a Go toolchain.
package demo

import _ "embed"

//go:embed tour.sh
var Script string
