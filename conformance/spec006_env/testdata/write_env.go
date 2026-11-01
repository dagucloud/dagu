package main

import (
	"fmt"
	"os"
	"strings"
)

// write_env writes NAME=value for each named variable in its own process
// environment, or NAME unset when the variable is absent.
func main() {
	if len(os.Args) < 3 {
		_, _ = fmt.Fprintf(os.Stderr, "usage: %s <path> <name>...\n", os.Args[0])
		os.Exit(2)
	}

	var out strings.Builder
	for _, name := range os.Args[2:] {
		if value, ok := os.LookupEnv(name); ok {
			_, _ = fmt.Fprintf(&out, "%s=%s\n", name, value)
		} else {
			_, _ = fmt.Fprintf(&out, "%s unset\n", name)
		}
	}
	if err := os.WriteFile(os.Args[1], []byte(out.String()), 0o600); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "write %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}
