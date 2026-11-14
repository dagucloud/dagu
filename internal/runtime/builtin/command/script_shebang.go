// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

func parseScriptShebang(script string) (string, []string, error) {
	line := script
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	line = strings.TrimSuffix(line, "\r")
	if !strings.HasPrefix(line, "#!") {
		return "", nil, nil
	}

	words, err := parseShebangWords(strings.TrimLeft(line[2:], " \t"))
	if err != nil {
		return "", nil, err
	}
	if len(words) == 0 || words[0] == "" {
		return "", nil, fmt.Errorf("shebang line has no interpreter command")
	}
	return words[0], words[1:], nil
}

func parseShebangWords(input string) ([]string, error) {
	var words []string
	var current strings.Builder
	wordStarted := false
	inSingle := false
	inDouble := false

	for i := 0; i < len(input); i++ {
		ch := input[i]
		switch {
		case inSingle:
			if ch == '\'' {
				inSingle = false
				wordStarted = true
				continue
			}
			current.WriteByte(ch)

		case inDouble:
			switch ch {
			case '"':
				inDouble = false
				wordStarted = true
			case '\\':
				if i+1 >= len(input) {
					return nil, fmt.Errorf("shebang line contains trailing unpaired backslash")
				}
				i++
				current.WriteByte(input[i])
				wordStarted = true
			default:
				current.WriteByte(ch)
				wordStarted = true
			}

		default:
			switch ch {
			case ' ', '\t':
				if wordStarted {
					words = append(words, current.String())
					current.Reset()
					wordStarted = false
				}
			case '\'':
				inSingle = true
				wordStarted = true
			case '"':
				inDouble = true
				wordStarted = true
			case '\\':
				if i+1 >= len(input) {
					return nil, fmt.Errorf("shebang line contains trailing unpaired backslash")
				}
				i++
				current.WriteByte(input[i])
				wordStarted = true
			default:
				current.WriteByte(ch)
				wordStarted = true
			}
		}
	}

	switch {
	case inSingle:
		return nil, fmt.Errorf("shebang line contains unterminated single quote")
	case inDouble:
		return nil, fmt.Errorf("shebang line contains unterminated double quote")
	}
	if wordStarted {
		words = append(words, current.String())
	}
	return words, nil
}

func resolveShebangExecutable(ctx context.Context, command string) (string, error) {
	if hasPathSeparator(command) {
		return command, nil
	}

	resolved, err := cmdutil.LookPathInEnv(command, runtime.AllEnvs(ctx))
	if err != nil {
		return "", fmt.Errorf("failed to resolve shebang interpreter %q in step PATH: %w", command, err)
	}
	return resolved, nil
}

func hasPathSeparator(path string) bool {
	return strings.Contains(path, "/") || strings.Contains(path, `\`)
}
