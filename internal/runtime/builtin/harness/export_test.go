// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import "github.com/dagucloud/dagu/internal/core"

func AgentConfigFromBuiltinHarnessConfigForTest(cfg map[string]any) (*core.AgentStepConfig, error) {
	return agentConfigFromBuiltinHarnessConfig(cfg)
}

func BuiltinRunCanceledForTest(stopped bool, runCtxErr, parentCtxErr error) bool {
	return builtinRunCanceled(stopped, runCtxErr, parentCtxErr)
}
