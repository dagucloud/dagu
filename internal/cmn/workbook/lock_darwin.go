// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package workbook

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// An owner file alone cannot distinguish a live workbook from a leftover
// on macOS. Keep the advisory warning when the process probe is inconclusive.
const lockProbeSupported = false

func isSharingViolation(error) bool            { return false }
func lockFileHeld(string) bool                 { return false }
func renameRefusedByHolder(string, error) bool { return false }

// workbookHeld checks the workbook itself because Excel need not keep the
// owner file open on macOS. This is best-effort detection, not a file lock.
func workbookHeld(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	//nolint:gosec // The executable is fixed; the path is a separate data argument after --, never shell input.
	output, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "+c", "0", "-Fc", "--", path).Output()
	return excelProcessHoldsWorkbook(output, err)
}

func excelProcessHoldsWorkbook(output []byte, err error) bool {
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		if line == "cMicrosoft Excel" {
			return true
		}
	}
	return false
}
