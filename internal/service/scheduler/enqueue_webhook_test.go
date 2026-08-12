// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueWebhookRun_PropagatesFindAttemptErrors(t *testing.T) {
	t.Parallel()

	store := &findAttemptErrStore{err: dagrun.ErrNoStatusData}
	err := EnqueueWebhookRun(
		context.Background(),
		persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{}),
		nil,
		t.TempDir(),
		t.TempDir(),
		"",
		&ir.DAG{Name: "ci"},
		"run-1",
		"",
		time.Now(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check existing webhook run")
	assert.True(t, errors.Is(err, dagrun.ErrNoStatusData))
}

type findAttemptErrStore struct {
	testutil.DAGRunBackendStub
	err error
}

func (s *findAttemptErrStore) FindAttempt(context.Context, ir.DAGRunRef) (dagrun.Attempt, error) {
	return nil, s.err
}
