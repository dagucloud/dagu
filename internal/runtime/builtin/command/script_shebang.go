// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/runtime"
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

	if pathEnv, ok := lookupEnv(runtime.AllEnvs(ctx), "PATH"); ok {
		resolved, err := lookPathInPATH(command, pathEnv)
		if err == nil {
			return resolved, nil
		}
		return "", fmt.Errorf("failed to resolve shebang interpreter %q in step PATH: %w", command, err)
	}

	if resolved, ok := cmdutil.FindExecutable(command); ok {
		return resolved, nil
	}
	return "", fmt.Errorf("failed to resolve shebang interpreter %q in PATH", command)
}

func hasPathSeparator(path string) bool {
	return strings.Contains(path, "/") || strings.Contains(path, `\`)
}

func lookupEnv(env []string, key string) (string, bool) {
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if goruntime.GOOS == "windows" {
			if strings.EqualFold(name, key) {
				return value, true
			}
			continue
		}
		if name == key {
			return value, true
		}
	}
	return "", false
}

func lookPathInPATH(command, pathEnv string) (string, error) {
	var lastErr error
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		for _, candidate := range pathCandidates(filepath.Join(dir, command)) {
			if isExecutableFile(candidate) {
				return candidate, nil
			}
			if _, err := os.Stat(candidate); err != nil {
				lastErr = err
			}
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", os.ErrNotExist
}

func pathCandidates(candidate string) []string {
	if goruntime.GOOS != "windows" || filepath.Ext(candidate) != "" {
		return []string{candidate}
	}

	pathext := os.Getenv("PATHEXT")
	if pathext == "" {
		pathext = ".COM;.EXE;.BAT;.CMD"
	}
	exts := strings.Split(pathext, ";")
	candidates := make([]string, 0, len(exts)+1)
	candidates = append(candidates, candidate)
	for _, ext := range exts {
		if ext == "" {
			continue
		}
		candidates = append(candidates, candidate+ext)
	}
	return candidates
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if goruntime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}
