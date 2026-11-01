// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package harness

import (
	"errors"
	"os/exec"
)

func setBatchCommandLine(*exec.Cmd, string) error {
	return errors.New("harness: batch files run only on Windows")
}
