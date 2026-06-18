package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		_, _ = fmt.Fprintf(os.Stderr, "usage: %s <path> <value>\n", os.Args[0])
		os.Exit(2)
	}

	if err := os.WriteFile(os.Args[1], []byte(os.Args[2]+"\n"), 0o600); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "write %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}
