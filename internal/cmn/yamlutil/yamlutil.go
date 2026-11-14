// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package yamlutil holds helpers that work around YAML parser quirks.
package yamlutil

import (
	"strings"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
)

// ClearEmptyDocumentSeparators blanks the `---` marker of an empty document
// that directly precedes another document marker, keeping line numbers intact.
// The YAML parser stops collecting documents at the first of two consecutive
// `---` markers, so every document after an empty one would otherwise be
// silently dropped. Clearing the earlier marker leaves a single separator, so
// all real documents remain visible to the parser.
func ClearEmptyDocumentSeparators(data []byte) []byte {
	src := string(data)
	var blankLines []int
	var prev *token.Token
	for _, tk := range lexer.Tokenize(src) {
		if tk.Type == token.CommentType {
			continue
		}
		if tk.Type == token.DocumentHeaderType &&
			prev != nil && prev.Type == token.DocumentHeaderType && prev.Position != nil {
			blankLines = append(blankLines, prev.Position.Line)
		}
		prev = tk
	}
	if len(blankLines) == 0 {
		return data
	}

	lines := strings.Split(src, "\n")
	for _, line := range blankLines {
		content := lines[line-1]
		if i := strings.IndexByte(content, '#'); i >= 0 {
			content = content[:i]
		}
		if strings.TrimSpace(content) == "---" {
			lines[line-1] = ""
		}
	}
	return []byte(strings.Join(lines, "\n"))
}
