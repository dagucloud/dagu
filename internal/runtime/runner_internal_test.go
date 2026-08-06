// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/incremental"
	filematerialization "github.com/dagucloud/dagu/v2/internal/persis/file/materialization"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternalStepRetryEnabled(t *testing.T) {
	t.Run("DisabledByDefault", func(t *testing.T) {
		ctx := exec.NewContext(context.Background(), &core.DAG{Name: "test"}, "run-1", "test.log")
		assert.False(t, externalStepRetryEnabled(ctx))
	})

	t.Run("EnabledByProcessEnv", func(t *testing.T) {
		t.Setenv(exec.EnvKeyExternalStepRetry, "1")
		ctx := exec.NewContext(context.Background(), &core.DAG{Name: "test"}, "run-1", "test.log")
		assert.True(t, externalStepRetryEnabled(ctx))
	})

	t.Run("EnabledByExecutionContextEnv", func(t *testing.T) {
		_ = os.Unsetenv(exec.EnvKeyExternalStepRetry)
		ctx := exec.NewContext(
			context.Background(),
			&core.DAG{Name: "test"},
			"run-1",
			"test.log",
			exec.WithEnvVars(exec.EnvKeyExternalStepRetry+"=1"),
		)
		assert.True(t, externalStepRetryEnabled(ctx))
	})
}

func TestRunNodeExecution_ExternalStepRetrySkipsRepeatBookkeeping(t *testing.T) {
	t.Parallel()

	step := core.Step{
		Name: "retrying-step",
		Commands: []core.CommandEntry{
			{Command: "exit", Args: []string{"1"}, CmdWithArgs: "exit 1"},
		},
		RetryPolicy: core.RetryPolicy{
			Limit:    1,
			Interval: 5 * time.Second,
		},
		RepeatPolicy: core.RepeatPolicy{
			RepeatMode: core.RepeatModeWhile,
			Interval:   time.Millisecond,
		},
	}
	plan, err := NewPlan(step)
	require.NoError(t, err)

	node := plan.GetNodeByName(step.Name)
	require.NotNil(t, node)

	logDir := t.TempDir()
	runner := New(&Config{
		DAGRunID: "run-1",
		LogDir:   logDir,
	})
	ctx := NewContext(
		context.Background(),
		&core.DAG{Name: "retry-dag", WorkingDir: logDir},
		"run-1",
		filepath.Join(logDir, "dag.log"),
		exec.WithEnvVars(exec.EnvKeyExternalStepRetry+"=1"),
	)
	require.NoError(t, node.Prepare(ctx, logDir, "run-1"))

	runner.runNodeExecution(ctx, plan, node, nil)
	require.NoError(t, node.Teardown())

	assert.Equal(t, core.NodeRetrying, node.State().Status)
	assert.Equal(t, 0, node.State().DoneCount)
	assert.Equal(t, 1, node.State().RetryCount)
}

func TestSetupVariables_StepEnvEvaluatesSequentiallyWithRuntimeVars(t *testing.T) {
	t.Parallel()

	envs := []string{
		"WORK_DIR=${DAG_RUN_ARTIFACTS_DIR}",
		"CURRENT_IDEA_PATH=${WORK_DIR}/current_idea.md",
	}
	tests := []struct {
		name         string
		step         core.Step
		dagContainer *core.Container
	}{
		{
			name: "step env",
			step: core.Step{
				Name: "render",
				Env:  envs,
			},
		},
		{
			name: "step container env",
			step: core.Step{
				Name:      "render",
				Container: &core.Container{Env: envs},
			},
		},
		{
			name: "dag container fallback env",
			step: core.Step{Name: "render"},
			dagContainer: &core.Container{
				Env: envs,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifactDir := filepath.Join(t.TempDir(), "artifacts", "run-1")
			plan, err := NewPlan(tt.step)
			require.NoError(t, err)
			node := plan.GetNodeByName(tt.step.Name)
			require.NotNil(t, node)

			runner := New(&Config{})
			ctx := NewContext(
				context.Background(),
				&core.DAG{
					Name:       "test-dag",
					WorkingDir: t.TempDir(),
					Container:  tt.dagContainer,
				},
				"run-1",
				filepath.Join(t.TempDir(), "dag.log"),
				WithArtifactDir(artifactDir),
			)

			ctx, err = runner.setupVariables(ctx, plan, node)
			require.NoError(t, err)

			result := AllEnvsMap(ctx)
			assert.Equal(t, artifactDir, result["WORK_DIR"])
			assert.Equal(t, filepath.Join(artifactDir, "current_idea.md"), filepath.Clean(result["CURRENT_IDEA_PATH"]))
		})
	}
}

func TestPrepareIncrementalPlanInfersFileDependency(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	runWorkDir := t.TempDir()
	producer := core.Step{
		ID:      "producer",
		Name:    "producer",
		Outputs: []core.StepOutputDeclaration{{Name: "artifact", Path: "artifact.txt"}},
	}
	consumer := core.Step{
		ID:     "consumer",
		Name:   "consumer",
		Inputs: []core.StepInputDeclaration{{Name: "artifact", Path: "./artifact.txt"}},
	}
	plan, err := NewPlan(producer, consumer)
	require.NoError(t, err)
	ctx := NewContext(context.Background(), &core.DAG{
		Name:       "incremental-test",
		Type:       core.TypeIncremental,
		WorkingDir: workingDir,
	}, "run-1", filepath.Join(workingDir, "dag.log"), WithWorkDir(runWorkDir))

	require.NoError(t, prepareIncrementalPlan(ctx, plan))
	producerNode := plan.GetNodeByName("producer")
	consumerNode := plan.GetNodeByName("consumer")
	require.NotNil(t, producerNode)
	require.NotNil(t, consumerNode)
	assert.True(t, plan.IsInferredDependency(producerNode.ID(), consumerNode.ID()))
	expectedOutput, err := incremental.ResolvePath(filepath.Join(workingDir, "artifact.txt"), "", true)
	require.NoError(t, err)
	assert.Equal(t, expectedOutput, producerNode.Step().Outputs[0].Path)
	assert.Equal(t, producerNode.Step().Outputs[0].Path, consumerNode.Step().Inputs[0].Path)
}

func TestIncrementalInputIsAvailableToStepPrecondition(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	inputPath := filepath.Join(workingDir, "source.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("source"), 0o600))
	step := core.Step{
		ID:            "build",
		Name:          "build",
		Inputs:        []core.StepInputDeclaration{{Name: "source", Path: inputPath}},
		Outputs:       []core.StepOutputDeclaration{{Name: "artifact", Path: filepath.Join(workingDir, "artifact.txt")}},
		Preconditions: []*core.Condition{{Condition: `test -f "${inputs.source}"`}},
	}
	plan, err := NewPlan(step)
	require.NoError(t, err)
	dag := &core.DAG{
		Name:               "incremental-test",
		Type:               core.TypeIncremental,
		WorkingDir:         workingDir,
		WorkingDirExplicit: true,
		Shell:              "sh",
	}
	ctx := NewContext(context.Background(), dag, "run-1", filepath.Join(workingDir, "dag.log"))
	runner := New(&Config{
		DAGRunID:             "run-1",
		MaterializationStore: filematerialization.New(filepath.Join(t.TempDir(), "materializations")),
	})
	node := plan.GetNodeByName(step.Name)
	require.NotNil(t, node)

	ctx, err = runner.setupVariables(ctx, plan, node)
	require.NoError(t, err)
	ctx = runner.setupNodeExecutionEnv(ctx, node)
	ctx, session, err := runner.startIncrementalSession(ctx, plan, node)
	require.NoError(t, err)
	require.NotNil(t, session)
	t.Cleanup(func() { require.NoError(t, session.Close("")) })

	assert.Equal(t, inputPath, GetEnv(ctx).Inputs["source"])
	require.NoError(t, node.evalPreconditions(ctx))
}

func TestPrepareIncrementalPlanRejectsInferredCycle(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	first := core.Step{
		ID:      "first",
		Name:    "first",
		Inputs:  []core.StepInputDeclaration{{Name: "second", Path: "second.txt"}},
		Outputs: []core.StepOutputDeclaration{{Name: "first", Path: "first.txt"}},
	}
	second := core.Step{
		ID:      "second",
		Name:    "second",
		Inputs:  []core.StepInputDeclaration{{Name: "first", Path: "first.txt"}},
		Outputs: []core.StepOutputDeclaration{{Name: "second", Path: "second.txt"}},
	}
	plan, err := NewPlan(first, second)
	require.NoError(t, err)
	ctx := NewContext(context.Background(), &core.DAG{
		Name:       "incremental-test",
		Type:       core.TypeIncremental,
		WorkingDir: workingDir,
	}, "run-1", filepath.Join(workingDir, "dag.log"))

	err = prepareIncrementalPlan(ctx, plan)
	require.ErrorIs(t, err, ErrCyclicPlan)
	firstNode := plan.GetNodeByName("first")
	secondNode := plan.GetNodeByName("second")
	require.NotNil(t, firstNode)
	require.NotNil(t, secondNode)
	assert.True(t, plan.IsInferredDependency(secondNode.ID(), firstNode.ID()))
	assert.False(t, plan.IsInferredDependency(firstNode.ID(), secondNode.ID()))
	assert.Empty(t, plan.Dependents(firstNode.ID()))
	assert.Equal(t, []int{secondNode.ID()}, plan.Dependencies(firstNode.ID()))
}
