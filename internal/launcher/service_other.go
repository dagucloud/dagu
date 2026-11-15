// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package launcher

import (
	"io"
	"os"
	"os/exec"
)

// LaunchOutsideService readies this process to launch runs that a stop of
// the Windows service running it leaves running. Service managers on other
// platforms are configured to leave runs running already, so it returns ""
// and launches stay as they are.
func LaunchOutsideService(string, string) (string, error) {
	return "", nil
}

func launchOutsideService(*exec.Cmd) {}

func serviceParentPID() int { return 0 }

// defaultOutput returns where a launched command writes when its spec names
// no output: the output of this process.
func defaultOutput() (stdout, stderr io.Writer) {
	return os.Stdout, os.Stderr
}
