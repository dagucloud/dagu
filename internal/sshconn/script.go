// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sshconn

import (
	"fmt"
	"strings"
)

// DefaultShell runs a remote script when neither the connection nor the step
// names a shell. It is POSIX, so every Unix host has it.
const DefaultShell = "/bin/sh"

// ShellCommand returns the command that starts shell with args on the
// server, which then reads its script from stdin.
func ShellCommand(shell string, args []string) string {
	if len(args) == 0 {
		return shell
	}
	return shell + " " + strings.Join(args, " ")
}

// Script wraps body in a function, so the shell reads the whole script before
// it runs any of it, however slowly stdin arrives.
func Script(body string) string {
	return fmt.Sprintf("__dagu_exec(){\n%s}\n__dagu_exec\n", body)
}
