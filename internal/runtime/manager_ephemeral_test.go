// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	procctrl "github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/testutil"
)

// freshProcess answers that one process entry is every DAG's freshest.
type freshProcess struct{ entry procctrl.ProcEntry }

func (freshProcess) ListAlive(context.Context, string) ([]ir.DAGRunRef, error) { return nil, nil }
func (freshProcess) IsRunAlive(context.Context, string, ir.DAGRunRef) (bool, error) {
	return false, nil
}
func (freshProcess) IsAttemptAlive(context.Context, string, ir.DAGRunRef, string) (bool, error) {
	return false, nil
}
func (p freshProcess) LatestFreshEntryByDAGName(context.Context, string, string) (*procctrl.ProcEntry, error) {
	entry := p.entry
	return &entry, nil
}

// An ephemeral run's process entry stays fresh for a moment after the run
// ends, as any run's does. The DAG's latest status is still the newest run
// of its history, which ephemeral runs never reach.
func TestLatestStatusSkipsAFreshEphemeralRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag, err := spec.LoadYAML(ctx, []byte("name: eph\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	root := t.TempDir()
	repo := testutil.NewFileDAGRunRepositoryWithEphemeral(filepath.Join(root, "dag-runs"),
		filepath.Join(root, "dag-runs-ephemeral"), persis.DAGRunRepositoryOptions{})
	write := func(runID string, ephemeral bool) string {
		attempt, err := repo.CreateAttempt(ctx, dag, time.Now(), runID, persis.DAGRunCreateAttemptOptions{Ephemeral: ephemeral})
		require.NoError(t, err)
		require.NoError(t, attempt.Open(ctx))
		require.NoError(t, attempt.Write(ctx, ir.NewStatusBuilder(dag).Create(runID, ir.Succeeded, 0, time.Now(),
			ir.WithAttemptID(attempt.ID()), ir.WithEphemeral(ephemeral))))
		require.NoError(t, attempt.Close(ctx))
		return attempt.ID()
	}
	write("normal", false)
	ephemeral := write("ephemeral", true)
	processes := freshProcess{procctrl.ProcEntry{Fresh: true, Meta: procctrl.ProcMeta{
		Name: dag.Name, DAGRunID: "ephemeral", AttemptID: ephemeral, RootName: dag.Name, RootDAGRunID: "ephemeral",
	}}}

	mgr := runtime.NewManager(repo, processes, &config.Config{})
	latest, err := mgr.GetLatestStatus(ctx, dag)

	require.NoError(t, err)
	require.Equal(t, "normal", latest.DAGRunID)
}
