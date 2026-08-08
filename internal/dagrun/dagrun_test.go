// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

func TestListDAGRunStatusesOptions(t *testing.T) {
	from := dagrun.NewUTC(time.Now().Add(-24 * time.Hour))
	to := dagrun.NewUTC(time.Now())
	statuses := []ir.Status{ir.Succeeded, ir.Failed}

	opts := dagrun.ListDAGRunStatusesOptions{}

	// Apply options
	dagrun.WithFrom(from)(&opts)
	dagrun.WithTo(to)(&opts)
	dagrun.WithStatuses(statuses)(&opts)
	dagrun.WithExactName("test-dag")(&opts)
	dagrun.WithName("partial-name")(&opts)
	dagrun.WithDAGRunID("run-123")(&opts)
	dagrun.WithAllHistory()(&opts)

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
	rootDAGRun := &ir.DAGRunRef{
		Name: "root-dag",
		ID:   "root-run-123",
	}

	opts := dagrun.NewDAGRunAttemptOptions{
		RootDAGRun: rootDAGRun,
		Retry:      true,
	}

	assert.Equal(t, rootDAGRun, opts.RootDAGRun)
	assert.True(t, opts.Retry)
}
