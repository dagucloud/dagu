// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/internal/tools/llmsgen"
)

func main() {
	sourceDir := flag.String("source", filepath.Join("skills", "dagu"), "directory containing the bundled Dagu skill")
	sourcePrefix := flag.String("source-prefix", filepath.ToSlash(filepath.Join("skills", "dagu")), "source path prefix written into llms.txt")
	outputPath := flag.String("output", "llms.txt", "path to write")
	flag.Parse()

	err := llmsgen.WriteFile(*outputPath, llmsgen.Options{
		SourceDir:    *sourceDir,
		SourcePrefix: *sourcePrefix,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
