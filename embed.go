// Package brinedalmanac holds the files at the repository root that both the Rust and Go
// implementations read, so there is one copy of each.
package brinedalmanac

import _ "embed"

// ClassesTOML is classes.toml, the failure-class table.
//
//go:embed classes.toml
var ClassesTOML []byte
