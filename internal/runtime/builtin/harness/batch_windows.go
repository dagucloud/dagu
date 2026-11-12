// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package harness

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// setBatchCommandLine runs cmd as cmd.exe with line as its exact command
// line, so Windows does not rebuild one from cmd.Args.
func setBatchCommandLine(cmd *exec.Cmd, line string) error {
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return fmt.Errorf("harness: locate cmd.exe: %w", err)
	}
	cmd.Path = filepath.Join(systemDir, "cmd.exe")
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = line
	return nil
}
