// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package outputs

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputsWritePublishesValues(t *testing.T) {
	t.Parallel()

	exec, err := newExecutor(context.Background(), core.Step{
		ExecutorConfig: core.ExecutorConfig{
			Type: "outputs",
			Config: map[string]any{
				"values": map[string]any{
					"messageId": "msg-123",
					"accepted":  true,
				},
			},
		},
		Commands: []core.CommandEntry{{Command: "write"}},
	})
	require.NoError(t, err)
	require.NoError(t, exec.Run(context.Background()))

	provider, ok := exec.(executor.OutputsProvider)
	require.True(t, ok)
	assert.Equal(t, map[string]any{
		"messageId": "msg-123",
		"accepted":  true,
	}, provider.GetOutputs())
}

func TestOutputsWriteRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	_, err := newExecutor(context.Background(), core.Step{
		ExecutorConfig: core.ExecutorConfig{
			Type:   "outputs",
			Config: map[string]any{"values": map[string]any{}},
		},
		Commands: []core.CommandEntry{{Command: "write"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "values must not be empty")
}
