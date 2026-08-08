// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/stretchr/testify/assert"
)

func TestAgentCurrentAutoRetryCount(t *testing.T) {
	t.Parallel()

	t.Run("InitialAttemptStartsAtZero", func(t *testing.T) {
		t.Parallel()
		a := &Agent{}
		assert.Equal(t, 0, a.currentAutoRetryCount())
	})

	t.Run("RetryTargetPreservesPersistedCount", func(t *testing.T) {
		t.Parallel()
		a := &Agent{
			retryTarget: &dagrun.DAGRunStatus{AutoRetryCount: 1},
		}
		assert.Equal(t, 1, a.currentAutoRetryCount())
	})
}
