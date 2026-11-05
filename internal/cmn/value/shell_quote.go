// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"slices"
	"strings"
)

// shellContext is the quoting context of a byte in a POSIX shell command.
type shellContext int

const (
	shellUnquoted shellContext = iota
	// shellSingleQuoted is inside '...' or $'...': the shell expands nothing.
	shellSingleQuoted
	// shellDoubleQuoted is inside "...": the shell expands parameters but
	// not a leading ~.
	shellDoubleQuoted
	// shellHereDoc is inside a here-document body, where quotes are literal.
	shellHereDoc
)

// shellFrame is one level of nested shell quoting or substitution.
type shellFrame int

const (
	frameUnquoted shellFrame = iota
	frameSingle
	frameANSIC        // $'...': single-quoted, with backslash escapes
	frameDouble       // "..."
	frameSubstitution // $( ... ): unquoted until its closing parenthesis
	frameSubshell     // ( ... ) inside a substitution
	frameBackquote    // ` ... `
	frameBrace        // ${ ... }: quotes in its word nest inside it
	frameBraceInDouble
	frameArithmetic // $(( ... )) or (( ... )): << is a shift, not a here-document
	frameArithParen // ( ... ) inside arithmetic
)

// hereDoc is a here-document whose body starts after the current line.
type hereDoc struct {
	delimiter string
	stripTabs bool // <<- strips leading tabs from each body line
}

// shellContextAt reports the quoting context of the byte at idx in a POSIX
// shell command. It follows single, double, and $'...' quotes, backslash
// escapes, $(...) and backquoted substitutions, ${...} parameter expansions,
// comments, and here-documents, each of which changes how later quotes are
// read.
func shellContextAt(input string, idx int) shellContext {
	stack := []shellFrame{frameUnquoted}
	var pending []hereDoc
	for i := 0; i < idx; i++ {
		top := stack[len(stack)-1]
		c := input[i]
		switch top {
		case frameSingle:
			if c == '\'' {
				stack = stack[:len(stack)-1]
			}
			continue
		case frameANSIC:
			switch c {
			case '\\':
				i++
			case '\'':
				stack = stack[:len(stack)-1]
			}
			continue
		case frameDouble:
			switch {
			case c == '\\':
				i++
			case c == '"':
				stack = stack[:len(stack)-1]
			case c == '`':
				stack = append(stack, frameBackquote)
			case strings.HasPrefix(input[i:], "$(("):
				stack = append(stack, frameArithmetic)
				i += 2
			case c == '$' && next(input, i) == '(':
				stack = append(stack, frameSubstitution)
				i++
			case c == '$' && next(input, i) == '{':
				stack = append(stack, frameBraceInDouble)
				i++
			}
			continue
		case frameArithmetic, frameArithParen:
			switch {
			case c == '(':
				stack = append(stack, frameArithParen)
			case c == ')' && top == frameArithParen:
				stack = stack[:len(stack)-1]
			case c == ')' && next(input, i) == ')':
				stack = stack[:len(stack)-1]
				i++
			}
			continue
		case frameBrace, frameBraceInDouble:
			switch {
			case c == '\\':
				i++
			case c == '}':
				stack = stack[:len(stack)-1]
			case c == '"':
				stack = append(stack, frameDouble)
			case c == '\'' && top == frameBrace:
				// Inside "${...}" an apostrophe is literal.
				stack = append(stack, frameSingle)
			case c == '$' && next(input, i) == '(':
				stack = append(stack, frameSubstitution)
				i++
			case c == '$' && next(input, i) == '{':
				stack = append(stack, frameBrace)
				i++
			}
			continue
		case frameUnquoted, frameSubstitution, frameSubshell, frameBackquote:
			// A command context, read below.
		}

		// A command context: unquoted, a substitution, or backquotes.
		switch {
		case c == '\\':
			i++
		case c == '\'':
			stack = append(stack, frameSingle)
		case c == '"':
			stack = append(stack, frameDouble)
		case c == '$' && next(input, i) == '\'':
			stack = append(stack, frameANSIC)
			i++
		case strings.HasPrefix(input[i:], "$(("):
			stack = append(stack, frameArithmetic)
			i += 2
		case c == '(' && next(input, i) == '(' && startsWord(input, i):
			stack = append(stack, frameArithmetic)
			i++
		case c == '$' && next(input, i) == '(':
			stack = append(stack, frameSubstitution)
			i++
		case c == '$' && next(input, i) == '{':
			stack = append(stack, frameBrace)
			i++
		case c == '(' && (top == frameSubstitution || top == frameSubshell):
			stack = append(stack, frameSubshell)
		case c == ')' && (top == frameSubstitution || top == frameSubshell):
			stack = stack[:len(stack)-1]
		case c == '`' && top == frameBackquote:
			stack = stack[:len(stack)-1]
		case c == '`':
			stack = append(stack, frameBackquote)
		case c == '#' && startsWord(input, i):
			// A comment runs to the end of the line.
			end := strings.IndexByte(input[i:], '\n')
			if end < 0 || i+end >= idx {
				return shellUnquoted
			}
			i += end - 1
		case c == '<' && strings.HasPrefix(input[i:], "<<<"):
			i += 2
		case c == '<' && next(input, i) == '<':
			doc, width := parseHereDoc(input[i+2:])
			if doc.delimiter != "" {
				pending = append(pending, doc)
			}
			i += 1 + width
		case c == '\n' && len(pending) > 0:
			bodyEnd, inBody := skipHereDocs(input, i+1, idx, pending)
			if inBody {
				return shellHereDoc
			}
			pending = nil
			i = bodyEnd - 1
		}
	}
	return contextOf(stack)
}

// contextOf maps the innermost frames to the context of the next byte. A
// parameter expansion keeps the context around it; a substitution starts a
// new, unquoted one.
func contextOf(stack []shellFrame) shellContext {
	for _, frame := range slices.Backward(stack) {
		switch frame {
		case frameSingle, frameANSIC:
			return shellSingleQuoted
		case frameDouble:
			return shellDoubleQuoted
		case frameBrace, frameBraceInDouble, frameArithmetic, frameArithParen:
			continue
		case frameUnquoted, frameSubstitution, frameSubshell, frameBackquote:
			return shellUnquoted
		}
	}
	return shellUnquoted
}

func next(input string, i int) byte {
	if i+1 < len(input) {
		return input[i+1]
	}
	return 0
}

// startsWord reports whether the byte at i begins a shell word, where a #
// starts a comment.
func startsWord(input string, i int) bool {
	if i == 0 {
		return true
	}
	return strings.IndexByte(" \t\n;&|()", input[i-1]) >= 0
}

// parseHereDoc reads the [-]word after << and returns the here-document and
// the number of bytes it used. Quotes in the word are removed, as the shell
// does when it matches the delimiter.
func parseHereDoc(rest string) (hereDoc, int) {
	var doc hereDoc
	i := 0
	if i < len(rest) && rest[i] == '-' {
		doc.stripTabs = true
		i++
	}
	for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
		i++
	}
	var word strings.Builder
	for i < len(rest) && strings.IndexByte(" \t\n;&|<>()", rest[i]) < 0 {
		switch c := rest[i]; c {
		case '\'', '"':
			if end := strings.IndexByte(rest[i+1:], c); end >= 0 {
				word.WriteString(rest[i+1 : i+1+end])
				i += end + 2
				continue
			}
		case '\\':
			i++
			if i < len(rest) {
				word.WriteByte(rest[i])
			}
			i++
			continue
		}
		word.WriteByte(rest[i])
		i++
	}
	doc.delimiter = word.String()
	return doc, i
}

// skipHereDocs walks the bodies of the pending here-documents from start. It
// reports whether idx falls inside a body and, if not, where the text after
// the last body begins.
func skipHereDocs(input string, start, idx int, pending []hereDoc) (int, bool) {
	pos := start
	for _, doc := range pending {
		for pos < len(input) {
			end := strings.IndexByte(input[pos:], '\n')
			lineEnd := len(input)
			if end >= 0 {
				lineEnd = pos + end
			}
			line := input[pos:lineEnd]
			if doc.stripTabs {
				line = strings.TrimLeft(line, "\t")
			}
			if line == doc.delimiter {
				pos = lineEnd + 1
				break
			}
			if idx < lineEnd+1 {
				return 0, true
			}
			pos = lineEnd + 1
		}
		if pos > idx {
			// idx is on the delimiter line itself.
			return pos, false
		}
	}
	return pos, false
}
