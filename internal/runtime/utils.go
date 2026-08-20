// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// parseExitCodeFromError attempts to extract an exit code from an error string.
// It looks for patterns like "exit status N" and extracts the numeric value.
// Returns the exit code and a boolean indicating if an exit code was found.
func parseExitCodeFromError(errStr string) (int, bool) {
	if !strings.Contains(errStr, "exit status") {
		return 0, false
	}

	// Look for the last occurrence of "exit status" followed by a number
	parts := strings.Split(errStr, "exit status ")
	if len(parts) <= 1 {
		return 0, false
	}

	// Get the last part and extract the number
	lastPart := parts[len(parts)-1]

	// Extract the number from the beginning of the string
	var numStr strings.Builder
	for _, ch := range lastPart {
		if ch >= '0' && ch <= '9' {
			numStr.WriteRune(ch)
		} else {
			break
		}
	}

	if numStr.Len() == 0 {
		return 0, false
	}

	code, err := strconv.Atoi(numStr.String())
	if err != nil {
		return 0, false
	}

	return code, true
}

// exitCodeFromError tries to extract an exit code from common error forms.
// It understands exec.ExitError, "exit status N" strings, and "signal:" markers.
func exitCodeFromError(execErr error) (int, bool) {
	if execErr == nil {
		return 0, false
	}

	if exitErr, ok := errors.AsType[*exec.ExitError](execErr); ok {
		return exitErr.ExitCode(), true
	}

	errStr := execErr.Error()
	if code, found := parseExitCodeFromError(errStr); found {
		return code, true
	}

	if strings.Contains(errStr, "signal:") {
		return -1, true
	}

	return 0, false
}
