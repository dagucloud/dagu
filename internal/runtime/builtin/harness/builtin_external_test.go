// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/internal/runtime/builtin/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentConfigFromBuiltinHarnessConfigAcceptsTopLevelAliases(t *testing.T) {
	cfg, err := harness.AgentConfigFromBuiltinHarnessConfigForTest(map[string]any{
		"provider":       "builtin",
		"max-iterations": 12,
		"safe-mode":      false,
		"web-search": map[string]any{
			"enabled": true,
		},
	})
	require.NoError(t, err)

	assert.Equal(t, 12, cfg.MaxIterations)
	assert.False(t, cfg.SafeMode)
	require.NotNil(t, cfg.WebSearch)
	assert.True(t, cfg.WebSearch.Enabled)
}

func TestBuiltinRunCanceled(t *testing.T) {
	assert.True(t, harness.BuiltinRunCanceledForTest(true, nil, nil))
	assert.True(t, harness.BuiltinRunCanceledForTest(false, context.Canceled, nil))
	assert.True(t, harness.BuiltinRunCanceledForTest(false, nil, context.Canceled))
	assert.False(t, harness.BuiltinRunCanceledForTest(false, nil, nil))
	assert.False(t, harness.BuiltinRunCanceledForTest(false, errors.New("failed"), nil))
}
