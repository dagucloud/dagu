// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dagrun/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/stretchr/testify/require"
)

func TestEnqueueRunDoesNotSeedQueuedCondition(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := &ir.DAG{Name: "queued-condition"}
	ir.InitializeDefaults(dag)
	dagRunStore := filedagrun.New(filepath.Join(tmp, "dag-runs"), filedagrun.WithLatestStatusToday(false))
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmp, "queue")))
	now := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)

	_, err := intake.EnqueueRun(ctx, intake.QueueRequest{
		DAGRunStore: dagRunStore,
		QueueStore:  queueStore,
		DAG:         dag,
		DAGRunID:    "run-1",
		LogBaseDir:  filepath.Join(tmp, "logs"),
		Now:         func() time.Time { return now },
	})
	require.NoError(t, err)

	attempt, err := dagRunStore.FindAttempt(ctx, dagrun.NewDAGRunRef(dag.Name, "run-1"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, ir.Queued, status.Status)
	require.Empty(t, status.Conditions)
}
