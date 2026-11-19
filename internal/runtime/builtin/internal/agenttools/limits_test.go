// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

// The limits of a step come from the step, else from the keys agent_tools.* of the
// Dagu configuration, else from the defaults. A value of 0 on the step or in the
// configuration means no limit of the size of a result.
func TestLimitsOrder(t *testing.T) {
	t.Parallel()

	plain := t.Context()
	assert.Equal(t, 64, MaxTools(plain, nil), "no configuration: the default")
	assert.Equal(t, 524288, ToolResultMaxBytes(plain, nil))

	configured := config.WithConfig(plain, &config.Config{AgentTools: config.AgentToolsConfig{MaxTools: 7, ToolResultMaxBytes: 0}})
	assert.Equal(t, 7, MaxTools(configured, &ir.LLMConfig{}), "a step with no key uses the configuration")
	assert.Equal(t, 0, ToolResultMaxBytes(configured, &ir.LLMConfig{}))

	three, hundred := 3, 100
	step := &ir.LLMConfig{MaxTools: &three, ToolResultMaxBytes: &hundred}
	assert.Equal(t, 3, MaxTools(configured, step), "a value on the step wins")
	assert.Equal(t, 100, ToolResultMaxBytes(configured, step))
}
