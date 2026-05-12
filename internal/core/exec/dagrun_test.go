// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
)

func TestListDAGRunStatusesOptions(t *testing.T) {
	from := exec.NewUTC(time.Now().Add(-24 * time.Hour))
	to := exec.NewUTC(time.Now())
	statuses := []core.Status{core.Succeeded, core.Failed}

	opts := exec.ListDAGRunStatusesOptions{}

	// Apply options
	exec.WithFrom(from)(&opts)
	exec.WithTo(to)(&opts)
	exec.WithStatuses(statuses)(&opts)
	exec.WithExactName("test-dag")(&opts)
	exec.WithName("partial-name")(&opts)
	exec.WithDAGRunID("run-123")(&opts)
	exec.WithAllHistory()(&opts)

	// Verify options were set correctly
	assert.Equal(t, from, opts.From)
	assert.Equal(t, to, opts.To)
	assert.Equal(t, statuses, opts.Statuses)
	assert.Equal(t, "test-dag", opts.ExactName)
	assert.Equal(t, "partial-name", opts.Name)
	assert.Equal(t, "run-123", opts.DAGRunID)
	assert.True(t, opts.AllHistory)
}

func TestNewDAGRunAttemptOptions(t *testing.T) {
	rootDAGRun := &exec.DAGRunRef{
		Name: "root-dag",
		ID:   "root-run-123",
	}

	opts := exec.NewDAGRunAttemptOptions{
		RootDAGRun: rootDAGRun,
		Retry:      true,
	}

	assert.Equal(t, rootDAGRun, opts.RootDAGRun)
	assert.True(t, opts.Retry)
}

func TestDAGRunAttemptRefIsSubDAGWhenRootAndRunShareID(t *testing.T) {
	ref := exec.DAGRunAttemptRef{
		DAGRun: exec.NewDAGRunRef("child-dag", "shared-run"),
		Root:   exec.NewDAGRunRef("root-dag", "shared-run"),
	}

	assert.True(t, ref.IsSubDAG())
}
