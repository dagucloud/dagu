// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// describeAction names a step's action for a finding, such as
// "action ssh.run" or "command" for a run step.
func describeAction(step ir.Step) string {
	var what string
	switch step.ExecutorConfig.Type {
	case "", "command", "shell":
		what = "command"
	default:
		what = "action " + spec.ActionName(&step)
	}
	if step.Nodes != nil {
		what += " on nodes"
	}
	return what
}
