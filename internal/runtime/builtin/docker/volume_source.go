// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type volumeSource struct {
	value  string
	isBind bool
}

func resolveVolumeSource(workDir, source, fieldPath string) (volumeSource, error) {
	expanded, err := expandHostEnv(source, fieldPath)
	if err != nil {
		return volumeSource{}, err
	}

	if isPathLikeVolumeSource(expanded) {
		resolved, err := resolveBindSource(workDir, expanded)
		if err != nil {
			return volumeSource{}, fmt.Errorf("%s: failed to resolve volume source %q: %w", fieldPath, source, err)
		}
		return volumeSource{value: resolved, isBind: true}, nil
	}

	return volumeSource{value: expanded}, nil
}

func expandHostEnv(source, fieldPath string) (string, error) {
	var expanded strings.Builder
	for i := 0; i < len(source); {
		if source[i] != '$' {
			expanded.WriteByte(source[i])
			i++
			continue
		}

		name, next, err := parseEnvReference(source, i)
		if err != nil {
			return "", fmt.Errorf("%s: %w in volume source %q", fieldPath, err, source)
		}
		value, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("%s: unresolved environment variable %q in volume source %q", fieldPath, name, source)
		}
		expanded.WriteString(value)
		i = next
	}

	return expanded.String(), nil
}

func parseEnvReference(source string, start int) (name string, next int, err error) {
	if start+1 >= len(source) {
		return "", 0, fmt.Errorf("invalid environment variable reference")
	}
	if source[start+1] == '{' {
		end := strings.IndexByte(source[start+2:], '}')
		if end == -1 {
			return "", 0, fmt.Errorf("invalid environment variable reference")
		}
		name = source[start+2 : start+2+end]
		if !isEnvName(name) {
			return "", 0, fmt.Errorf("invalid environment variable reference")
		}
		return name, start + 3 + end, nil
	}

	if !isEnvNameStart(source[start+1]) {
		return "", 0, fmt.Errorf("invalid environment variable reference")
	}
	next = start + 2
	for next < len(source) && isEnvNameChar(source[next]) {
		next++
	}
	return source[start+1 : next], next, nil
}

func isEnvName(name string) bool {
	if name == "" || !isEnvNameStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isEnvNameChar(name[i]) {
			return false
		}
	}
	return true
}

func isEnvNameStart(ch byte) bool {
	return ch == '_' || ('A' <= ch && ch <= 'Z') || ('a' <= ch && ch <= 'z')
}

func isEnvNameChar(ch byte) bool {
	return isEnvNameStart(ch) || ('0' <= ch && ch <= '9')
}

func isPathLikeVolumeSource(source string) bool {
	return filepath.IsAbs(source) ||
		strings.HasPrefix(source, ".") ||
		strings.HasPrefix(source, "~") ||
		strings.ContainsAny(source, `/\`)
}

func resolveBindSource(workDir, source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", nil
	}
	if isWindowsDriveSource(source) || isDockerToolboxSource(source) {
		return source, nil
	}

	if strings.HasPrefix(source, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home directory: %w", err)
		}
		source = filepath.Join(homeDir, source[1:])
	} else if !filepath.IsAbs(source) {
		if workDir != "" {
			source = filepath.Join(workDir, source)
		}
	}

	absPath, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}
	return filepath.Clean(absPath), nil
}

func isWindowsDriveSource(source string) bool {
	return len(source) >= 3 &&
		isASCIIAlpha(source[0]) &&
		source[1] == ':' &&
		(source[2] == '\\' || source[2] == '/')
}

func isDockerToolboxSource(source string) bool {
	return len(source) >= 5 &&
		strings.HasPrefix(source, "//") &&
		isASCIIAlpha(source[2]) &&
		source[3] == ':' &&
		(source[4] == '\\' || source[4] == '/')
}

func isASCIIAlpha(ch byte) bool {
	return ('A' <= ch && ch <= 'Z') || ('a' <= ch && ch <= 'z')
}
