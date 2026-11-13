// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// A deleted service DAG must not keep its count for the life of the server.
func TestFailureStreaksForgetDAGsNotListed(t *testing.T) {
	t.Parallel()
	var streaks failureStreaks
	run := ir.DAGRunStatus{DAGRunID: "run-1", Status: ir.Failed}
	streaks.put("kept", run, 2)
	streaks.put("deleted", run, 3)

	streaks.retain(map[string]struct{}{"kept": {}})

	count, ok := streaks.get("kept", run)
	assert.True(t, ok)
	assert.Equal(t, 2, count)
	_, ok = streaks.get("deleted", run)
	assert.False(t, ok)
}
