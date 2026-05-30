// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

// The subprocess launcher moved to internal/launcher. This file re-exports it
// so existing in-repo callers keep compiling unchanged while call sites migrate
// to internal/launcher directly. It adds no behavior and should be removed once
// no caller references runtime.SubCmdBuilder / runtime.Run / etc.

import "github.com/dagucloud/dagu/internal/launcher"

// Re-exported launcher types.
type (
	SubCmdBuilder  = launcher.SubCmdBuilder
	CmdSpec        = launcher.CmdSpec
	StartResult    = launcher.StartResult
	StartOptions   = launcher.StartOptions
	EnqueueOptions = launcher.EnqueueOptions
	RestartOptions = launcher.RestartOptions
	CommandError   = launcher.CommandError
)

// Re-exported launcher constructors and runners.
var (
	NewSubCmdBuilder = launcher.NewSubCmdBuilder
	Run              = launcher.Run
	Start            = launcher.Start
	StartProcess     = launcher.StartProcess
)
