// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intgharness

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	testutil "github.com/dagucloud/dagu/internal/test"
)

type shellKind int

const (
	posixShell shellKind = iota
	powerShell
)

// Commands builds portable command snippets for integration DAG fixtures.
type Commands struct {
	shell shellKind
}

func defaultCommands() Commands {
	if runtime.GOOS == "windows" {
		return commandsForShell(powerShell)
	}
	return commandsForShell(posixShell)
}

// PortableCommands returns command snippets for the current test platform.
func PortableCommands() Commands {
	return defaultCommands()
}

func commandsForShell(shell shellKind) Commands {
	return Commands{shell: shell}
}

// Sleep returns a shell snippet that waits for d.
func (c Commands) Sleep(d time.Duration) string {
	if d <= 0 {
		d = time.Millisecond
	}
	if c.shell == powerShell {
		millis := d.Milliseconds()
		if millis <= 0 {
			millis = 1
		}
		return fmt.Sprintf("Start-Sleep -Milliseconds %d", millis)
	}
	return fmt.Sprintf("sleep %s", strconv.FormatFloat(d.Seconds(), 'f', -1, 64))
}

// WriteFile returns a shell snippet that writes content to path.
func (c Commands) WriteFile(path, content string) string {
	if c.shell == powerShell {
		return fmt.Sprintf("Set-Content -Path %s -Value %s -NoNewline", testutil.PowerShellQuote(path), testutil.PowerShellQuote(content))
	}
	return fmt.Sprintf("printf '%%s' %s > %s", testutil.PosixQuote(content), testutil.PosixQuote(path))
}

// EnvOutputWithSeparator prints environment variables joined by separator.
func (c Commands) EnvOutputWithSeparator(separator string, names ...string) string {
	if len(names) == 0 {
		if c.shell == powerShell {
			return "Write-Output ''"
		}
		return "printf ''"
	}

	if c.shell == powerShell {
		refs := make([]string, 0, len(names))
		for _, name := range names {
			refs = append(refs, "$env:"+name)
		}
		return fmt.Sprintf(
			"Write-Output ((@(%s) | ForEach-Object { if ($null -eq $_) { '' } else { [string]$_ } }) -join %s)",
			strings.Join(refs, ", "),
			testutil.PowerShellQuote(separator),
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

// WaitForFile returns a shell snippet that blocks until path exists.
func (c Commands) WaitForFile(path string) string {
	if c.shell == powerShell {
		return fmt.Sprintf("while (-not (Test-Path %s)) {\n  Start-Sleep -Milliseconds 50\n}", testutil.PowerShellQuote(path))
	}
	return fmt.Sprintf("while [ ! -f %s ]; do\n  sleep 0.05\ndone", testutil.PosixQuote(path))
}

// BlockUntil is an alias for WaitForFile in long-running integration steps.
func (c Commands) BlockUntil(path string) string {
	return c.WaitForFile(path)
}
