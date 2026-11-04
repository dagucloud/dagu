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

// freshProcesses answers that its entries, newest first, are every DAG's
// fresh ones.
type freshProcesses []procctrl.ProcEntry

func (freshProcesses) ListAlive(context.Context, string) ([]ir.DAGRunRef, error) { return nil, nil }
func (freshProcesses) IsRunAlive(context.Context, string, ir.DAGRunRef) (bool, error) {
	return false, nil
}
func (freshProcesses) IsAttemptAlive(context.Context, string, ir.DAGRunRef, string) (bool, error) {
	return false, nil
}
func (p freshProcesses) FreshEntriesByDAGName(context.Context, string, string) ([]procctrl.ProcEntry, error) {
	return p, nil
}

// An ephemeral run's process entry stays fresh for a moment after the run
// ends, as any run's does. It is passed over: the DAG's latest status is the
// next fresh run's, or else the newest run of its history, which ephemeral
// runs never reach.
func TestLatestStatusSkipsAFreshEphemeralRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag, err := spec.LoadYAML(ctx, []byte("name: eph\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	parentDAG, err := spec.LoadYAML(ctx, []byte("name: parent\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	root := t.TempDir()
	repo := testutil.NewFileDAGRunRepositoryWithEphemeral(filepath.Join(root, "dag-runs"),
		filepath.Join(root, "dag-runs-ephemeral"), persis.DAGRunRepositoryOptions{})
	write := func(dag *ir.DAG, runID string, opts persis.DAGRunCreateAttemptOptions) procctrl.ProcEntry {
		attempt, err := repo.CreateAttempt(ctx, dag, time.Now(), runID, opts)
		require.NoError(t, err)
		require.NoError(t, attempt.Open(ctx))
		require.NoError(t, attempt.Write(ctx, ir.NewStatusBuilder(dag).Create(runID, ir.Succeeded, 0, time.Now(),
			ir.WithAttemptID(attempt.ID()), ir.WithEphemeral(opts.Ephemeral))))
		require.NoError(t, attempt.Close(ctx))
		rootName, rootID := dag.Name, runID
		if !opts.RootDAGRun.Zero() {
			rootName, rootID = opts.RootDAGRun.Name, opts.RootDAGRun.ID
		}
		return procctrl.ProcEntry{Fresh: true, Meta: procctrl.ProcMeta{
			Name: dag.Name, DAGRunID: runID, AttemptID: attempt.ID(), RootName: rootName, RootDAGRunID: rootID,
		}}
	}
	write(dag, "normal", persis.DAGRunCreateAttemptOptions{})
	write(parentDAG, "parent", persis.DAGRunCreateAttemptOptions{})
	child := write(dag, "child", persis.DAGRunCreateAttemptOptions{RootDAGRun: ir.NewDAGRunRef("parent", "parent")})
	ephemeral := write(dag, "ephemeral", persis.DAGRunCreateAttemptOptions{Ephemeral: true})

	for name, tc := range map[string]struct {
		fresh freshProcesses
		want  string
	}{
		"to the history":    {freshProcesses{ephemeral}, "normal"},
		"to the next fresh": {freshProcesses{ephemeral, child}, "child"},
	} {
		t.Run(name, func(t *testing.T) {
			mgr := runtime.NewManager(repo, tc.fresh, &config.Config{})
			latest, err := mgr.GetLatestStatus(ctx, dag)

			require.NoError(t, err)
			require.Equal(t, tc.want, latest.DAGRunID)
		})
	}
}
