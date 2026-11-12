// Command brined-almanac is the Go port. It runs beside the Rust binary and acts on nothing.
// See docs/adrs/0001-port-to-go-alongside-rust.md.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "brined-almanac (go): read-only skeleton, nothing is ported yet")
	os.Exit(2)
}
