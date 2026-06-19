// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package test

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func ForOS(posix, windows string) string {
	if runtime.GOOS == "windows" {
		return windows
	}
	return posix
}

func JoinLines(lines ...string) string {
	nonEmpty := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		nonEmpty = append(nonEmpty, line)
	}
	return strings.Join(nonEmpty, "\n")
}

func JoinShellCommands(commands ...string) string {
	nonEmpty := make([]string, 0, len(commands))
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		nonEmpty = append(nonEmpty, command)
	}
	return strings.Join(nonEmpty, "; ")
}

func PosixQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func PowerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func ShellQuote(value string) string {
	return ForOS(PosixQuote(value), PowerShellQuote(value))
}

func ShellPath(path string) string {
	if runtime.GOOS == "windows" {
		return filepath.ToSlash(path)
	}
	return path
}

func Output(value string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("Write-Output %s", PowerShellQuote(value))
	}
	return fmt.Sprintf("printf '%%s\\n' %s", PosixQuote(value))
}

func OutputEscaped(value string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("[Console]::Out.Write([Text.RegularExpressions.Regex]::Unescape(%s))", PowerShellQuote(value))
	}
	return fmt.Sprintf("printf '%%b' %s", PosixQuote(value))
}

func Stderr(value string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("[Console]::Error.WriteLine(%s)", PowerShellQuote(value))
	}
	return fmt.Sprintf("printf '%%s\\n' %s 1>&2", PosixQuote(value))
}

func portableEnvNameFromRef(ref string) (string, bool) {
	var name string
	switch {
	case strings.HasPrefix(ref, "${") && strings.HasSuffix(ref, "}"):
		name = ref[2 : len(ref)-1]
	case strings.HasPrefix(ref, "$"):
		name = ref[1:]
	default:
		return "", false
	}

	if name == "" {
		return "", false
	}

	for i, r := range name {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return "", false
		}
	}

	return name, true
}

func portablePowerShellEnvExpr(ref string) (string, bool) {
	name, ok := portableEnvNameFromRef(ref)
	if !ok {
		return "", false
	}
	return "$env:" + name, true
}

// ExpandedOutput emits a Dagu-resolved ${...} value while keeping shell quoting
// valid on each platform. The input should be a Dagu reference, not an
// arbitrary literal string.
func ExpandedOutput(ref string) string {
	if runtime.GOOS == "windows" {
		if expr, ok := portablePowerShellEnvExpr(ref); ok {
			return "Write-Output " + expr
		}
		return fmt.Sprintf("Write-Output %s", PowerShellQuote(ref))
	}
	return fmt.Sprintf("printf '%%s\\n' \"%s\"", ref)
}

func LabeledExpandedOutput(prefix, ref string) string {
	if runtime.GOOS == "windows" {
		if expr, ok := portablePowerShellEnvExpr(ref); ok {
			return fmt.Sprintf("Write-Output (%s + [string](%s))", PowerShellQuote(prefix), expr)
		}
		return fmt.Sprintf("Write-Output (%s + %s)", PowerShellQuote(prefix), PowerShellQuote(ref))
	}
	return fmt.Sprintf("printf '%%s\\n' \"%s%s\"", prefix, ref)
}

func Sleep(d time.Duration) string {
	if runtime.GOOS == "windows" {
		millis := d.Milliseconds()
		if millis <= 0 {
			millis = 1
		}
		return fmt.Sprintf("Start-Sleep -Milliseconds %d", millis)
	}
	return fmt.Sprintf("sleep %s", strconv.FormatFloat(d.Seconds(), 'f', -1, 64))
}

func EnvOutputWithSeparator(separator string, names ...string) string {
	if len(names) == 0 {
		if runtime.GOOS == "windows" {
			return "Write-Output ''"
		}
		return "printf ''"
	}

	if runtime.GOOS == "windows" {
		refs := make([]string, 0, len(names))
		for _, name := range names {
			refs = append(refs, "$env:"+name)
		}
		return fmt.Sprintf(
			"Write-Output ((@(%s) | ForEach-Object { if ($null -eq $_) { '' } else { [string]$_ } }) -join %s)",
			strings.Join(refs, ", "),
			PowerShellQuote(separator),
		)
	}

	placeholders := make([]string, 0, len(names))
	values := make([]string, 0, len(names))
	for _, name := range names {
		placeholders = append(placeholders, "%s")
		values = append(values, fmt.Sprintf("${%s:-}", name))
	}
	return fmt.Sprintf("printf '%s' %s", strings.Join(placeholders, separator), strings.Join(values, " "))
}

func EnvOutput(names ...string) string {
	return EnvOutputWithSeparator("|", names...)
}
