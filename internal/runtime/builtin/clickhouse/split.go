// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package clickhouse

import "strings"

// splitStatements splits a script into statements at semicolons outside
// quoted text and comments. The HTTP interface runs one statement per
// request, so the executor sends each element separately. Statements keep
// their inner comments; empty statements and statements holding only
// comments are dropped.
//
// It understands the ClickHouse lexical forms that can hide a semicolon:
// '...' strings and "..." and `...` identifiers (a backslash or a doubled
// quote escapes the quote), $$...$$ and $tag$...$tag$ heredocs, -- and
// "# " / "#!" line comments, and nested /* ... */ block comments.
//
// Data written inline after INSERT ... FORMAT is not understood: a semicolon
// in it splits the statement.
func splitStatements(script string) []string {
	var (
		statements []string
		start      int
		meaningful bool
	)
	flush := func(end int) {
		if meaningful {
			if stmt := strings.TrimSpace(script[start:end]); stmt != "" {
				statements = append(statements, stmt)
			}
		}
		meaningful = false
	}

	n := len(script)
	for i := 0; i < n; {
		c := script[i]
		switch {
		case c == ';':
			flush(i)
			i++
			start = i
		case c == '\'' || c == '"' || c == '`':
			meaningful = true
			i = skipQuoted(script, i)
		case c == '-' && i+1 < n && script[i+1] == '-':
			i = skipLine(script, i)
		case c == '#' && i+1 < n && (script[i+1] == ' ' || script[i+1] == '!'):
			i = skipLine(script, i)
		case c == '/' && i+1 < n && script[i+1] == '*':
			// Block comments nest, as they do in the server's lexer.
			depth := 1
			for i += 2; i < n && depth > 0; {
				switch {
				case i+1 < n && script[i] == '/' && script[i+1] == '*':
					depth++
					i += 2
				case i+1 < n && script[i] == '*' && script[i+1] == '/':
					depth--
					i += 2
				default:
					i++
				}
			}
		case c == '$' && (i == 0 || !isIdentByte(script[i-1])):
			if tag, ok := heredocTag(script, i); ok {
				meaningful = true
				end := strings.Index(script[i+len(tag):], tag)
				if end < 0 {
					i = n
				} else {
					i += len(tag) + end + len(tag)
				}
				continue
			}
			meaningful = true
			i++
		default:
			if !isSpaceByte(c) {
				meaningful = true
			}
			i++
		}
	}
	flush(n)
	return statements
}

// skipQuoted returns the index after the quoted token starting at i.
func skipQuoted(s string, i int) int {
	quote := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case quote:
			if j+1 < len(s) && s[j+1] == quote {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(s)
}

// skipLine returns the index of the line break ending the comment at i.
func skipLine(s string, i int) int {
	if end := strings.IndexByte(s[i:], '\n'); end >= 0 {
		return i + end
	}
	return len(s)
}

// heredocTag returns the opening tag ($$ or $name$) at i.
func heredocTag(s string, i int) (string, bool) {
	j := i + 1
	if j < len(s) && s[j] == '$' {
		return "$$", true
	}
	if j < len(s) && (isLetterByte(s[j]) || s[j] == '_') {
		for j < len(s) && isIdentByte(s[j]) && s[j] != '$' {
			j++
		}
		if j < len(s) && s[j] == '$' {
			return s[i : j+1], true
		}
	}
	return "", false
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func isLetterByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isIdentByte(c byte) bool {
	return isLetterByte(c) || c >= '0' && c <= '9' || c == '_' || c == '$'
}
