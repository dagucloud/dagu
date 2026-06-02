// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/dag"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueExecutorPersistsInheritedProfile(t *testing.T) {
	t.Parallel()

	th := test.Setup(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{{Name: "default", MaxActiveRuns: 1}}
	}))

	parent := &core.DAG{
		Name: "parent",
		LocalDAGs: map[string]*core.DAG{
			"child": {
				Name:     "child",
				YamlData: []byte("name: child\nsteps:\n  - name: step\n    run: echo child\n"),
				Steps: []core.Step{
					{Name: "step", ExecutorConfig: core.ExecutorConfig{Type: "noop"}},
				},
			},
		},
	}
	parentRun := exec.NewDAGRunRef(parent.Name, "parent-run")
	ctx := runtime.NewContext(
		th.Context,
		parent,
		parentRun.ID,
		filepath.Join(th.Config.Paths.LogDir, "parent.log"),
		runtime.WithRootDAGRun(parentRun),
		runtime.WithDAGRunStore(th.DAGRunStore),
		runtime.WithQueueStore(th.QueueStore),
		runtime.WithDAGRunLogDir(th.Config.Paths.LogDir),
		runtime.WithDAGRunArtifactDir(th.Config.Paths.ArtifactDir),
		runtime.WithRuntimeProfile("prod", "", nil),
	)

	step := core.Step{
		Name:           "enqueue-child",
		ExecutorConfig: core.ExecutorConfig{Type: core.ExecutorTypeDAGEnqueue},
		SubDAG:         &core.SubDAG{Name: "child"},
	}
	execImpl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)

	dagExec, ok := execImpl.(executor.DAGExecutor)
	require.True(t, ok)
	dagExec.SetParams(executor.RunParams{RunID: "child-run", Params: "FOO=bar"})

	var stdout bytes.Buffer
	execImpl.SetStdout(&stdout)
	require.NoError(t, execImpl.Run(ctx))

	attempt, err := th.DAGRunStore.FindAttempt(ctx, exec.NewDAGRunRef("child", "child-run"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)

	assert.Equal(t, core.Queued, status.Status)
	assert.Equal(t, core.TriggerTypeSubDAG, status.TriggerType)
	assert.Equal(t, "prod", status.ProfileName)
	assert.Equal(t, exec.NewDAGRunRef("child", "child-run"), status.Root)
	assert.True(t, status.Parent.Zero())
}
