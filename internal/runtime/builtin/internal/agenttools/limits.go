// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Spec 090, "Limits". The keys agent_tools.max_tools and
// agent_tools.tool_result_max_bytes of the Dagu configuration are the defaults of
// each step of the process. A chat.completion step can set its own value. An
// agent.run step has no key for these limits, so it always uses the defaults.

// MaxTools returns the size limit of the effective catalog of a step: the value
// of the step, else the value of the configuration.
func MaxTools(ctx context.Context, cfg *ir.LLMConfig) int {
	if cfg != nil && cfg.MaxTools != nil {
		return *cfg.MaxTools
	}
	maxTools, _ := config.GetConfig(ctx).AgentTools.Limits()
	return maxTools
}

// ToolResultMaxBytes returns the size limit of one tool result of a step: the
// value of the step, else the value of the configuration. Zero means no limit.
func ToolResultMaxBytes(ctx context.Context, cfg *ir.LLMConfig) int {
	if cfg != nil && cfg.ToolResultMaxBytes != nil {
		return *cfg.ToolResultMaxBytes
	}
	_, resultMax := config.GetConfig(ctx).AgentTools.Limits()
	return resultMax
}
