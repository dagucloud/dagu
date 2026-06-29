// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/dagrun/intake"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/stretchr/testify/require"
)

func TestEnqueueRunPersistsQueuedCondition(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := &core.DAG{Name: "queued-condition"}
	core.InitializeDefaults(dag)
	dagRunStore := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
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

	attempt, err := dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-1"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Queued, status.Status)
	require.Equal(t, []exec.DAGRunCondition{
		exec.NewQueuedDAGRunCondition(
			"QueueAccepted",
			"DAG-run is waiting in the queue.",
			now,
		),
	}, status.Conditions)
}
