// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/service/history"
	"github.com/stretchr/testify/require"
)

func TestSubmitRunWritesQueuedLifecycleState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := &core.DAG{Name: "history-submit"}
	core.InitializeDefaults(dag)
	dagRunStore := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmp, "queue")))
	now := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)

	historySvc := history.New(history.Config{
		DAGRunStore: dagRunStore,
		QueueStore:  queueStore,
		LogBaseDir:  filepath.Join(tmp, "logs"),
		Now:         func() time.Time { return now },
	})
	submitted, err := historySvc.SubmitRun(ctx, history.SubmitRunCommand{
		DAG:      dag,
		DAGRunID: "run-1",
	})
	require.NoError(t, err)
	require.Equal(t, exec.NewDAGRunRef(dag.Name, "run-1"), submitted.DAGRun)

	attempt, err := dagRunStore.FindAttempt(ctx, submitted.DAGRun)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Queued, status.Status)
	require.Equal(t, submitted.Attempt.ID(), status.AttemptID)
	require.Empty(t, status.Conditions)
}
