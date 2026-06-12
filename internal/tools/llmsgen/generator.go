// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llmsgen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var sourceFiles = []string{
	"SKILL.md",
	filepath.Join("references", "steptypes.md"),
	filepath.Join("references", "dagu-action.md"),
	filepath.Join("references", "cli.md"),
	filepath.Join("references", "env.md"),
	filepath.Join("references", "codingagent.md"),
}

// Options configures llms.txt generation.
type Options struct {
	SourceDir    string
	SourcePrefix string
}

// Generate returns the generated llms.txt content for the bundled Dagu skill.
func Generate(opts Options) ([]byte, error) {
	sourceDir := opts.SourceDir
	if sourceDir == "" {
		sourceDir = filepath.Join("skills", "dagu")
	}

	prefix := cleanPrefix(opts.SourcePrefix)
	if prefix == "" {
		prefix = filepath.ToSlash(filepath.Clean(sourceDir))
	}

	var buf bytes.Buffer
	buf.WriteString("# Dagu\n\n")
	buf.WriteString("Dagu is a self-contained workflow orchestration engine for running DAGs defined in YAML. ")
	buf.WriteString("It runs as a single binary without requiring an external database or message broker. ")
	buf.WriteString("It stores state locally by default and supports local, queued, and distributed execution modes.\n\n")
	buf.WriteString("Use this compact reference when authoring, validating, or troubleshooting Dagu workflows with an AI agent. ")
	buf.WriteString("For the full human documentation, see https://docs.dagu.sh/.\n")

	for _, file := range sourceFiles {
		path := filepath.Join(sourceDir, file)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", joinLabel(prefix, file), err)
		}

		content := stripFrontMatter(string(data))
		buf.WriteString("\n---\n\n")
		buf.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			buf.WriteByte('\n')
		}
	}

	return buf.Bytes(), nil
}

// WriteFile writes generated llms.txt content to outputPath.
func WriteFile(outputPath string, opts Options) error {
	content, err := Generate(opts)
	if err != nil {
		return err
	}

	parent := filepath.Dir(outputPath)
	if parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", parent, err)
		}
	}

	if err := os.WriteFile(outputPath, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}
	return nil
}

func stripFrontMatter(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return strings.TrimLeft(content, "\n")
	}

	rest := strings.TrimPrefix(content, "---\n")
	_, after, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return strings.TrimLeft(content, "\n")
	}

	return strings.TrimLeft(after, "\n")
}

func cleanPrefix(prefix string) string {
	prefix = filepath.ToSlash(prefix)
	return strings.Trim(prefix, "/")
}

func joinLabel(prefix, file string) string {
	file = filepath.ToSlash(file)
	if prefix == "" {
		return file
	}
	return prefix + "/" + file
}
