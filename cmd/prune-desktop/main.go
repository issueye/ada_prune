// Command prune-desktop runs the gateway with its native management window: no
// webview, no front-end build, one binary.
package main

import (
	"fmt"
	"os"

	"github.com/learningdog1/ada_prune/internal/desktop"
)

func main() {
	if err := desktop.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "[prune-desktop] %v\n", err)
		os.Exit(1)
	}
}
