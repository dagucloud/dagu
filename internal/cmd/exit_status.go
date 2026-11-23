// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

// exitStatusError ends a command with an exit status other than 1. The
// binary's main reads the status through ExitStatus.
type exitStatusError struct {
	code int
	err  error
}

func (e *exitStatusError) Error() string { return e.err.Error() }

func (e *exitStatusError) Unwrap() error { return e.err }

// ExitStatus returns the exit status the command chose.
func (e *exitStatusError) ExitStatus() int { return e.code }
