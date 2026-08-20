// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	gort "runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"

	"github.com/dagucloud/dagu/v2/internal/build"
	"github.com/dagucloud/dagu/v2/internal/ir"
	filematerialization "github.com/dagucloud/dagu/v2/internal/persis/file/materialization"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternalStepRetryEnabled(t *testing.T) {
	t.Run("DisabledByDefault", func(t *testing.T) {
		ctx := runctx.NewContext(context.Background(), &ir.DAG{Name: "test"}, "run-1", "test.log")
		assert.False(t, externalStepRetryEnabled(ctx))
	})

	t.Run("EnabledByProcessEnv", func(t *testing.T) {
		t.Setenv(runenv.EnvKeyExternalStepRetry, "1")
		ctx := runctx.NewContext(context.Background(), &ir.DAG{Name: "test"}, "run-1", "test.log")
		assert.True(t, externalStepRetryEnabled(ctx))
	})

	t.Run("EnabledByExecutionContextEnv", func(t *testing.T) {
		_ = os.Unsetenv(runenv.EnvKeyExternalStepRetry)
		ctx := runctx.NewContext(
			context.Background(),
			&ir.DAG{Name: "test"},
			"run-1",
			"test.log",
			runctx.WithEnvVars(runenv.EnvKeyExternalStepRetry+"=1"),
		)
		assert.True(t, externalStepRetryEnabled(ctx))
	})
}

func TestRunNodeExecution_ExternalStepRetrySkipsRepeatBookkeeping(t *testing.T) {
	t.Parallel()

	step := ir.Step{
		Name: "retrying-step",
		Commands: []ir.CommandEntry{
			{Command: "exit", Args: []string{"1"}, CmdWithArgs: "exit 1"},
		},
		RetryPolicy: ir.RetryPolicy{
			Limit:    1,
			Interval: 5 * time.Second,
		},
		RepeatPolicy: ir.RepeatPolicy{
			RepeatMode: ir.RepeatModeWhile,
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
		&ir.DAG{Name: "retry-dag", WorkingDir: logDir},
		"run-1",
		filepath.Join(logDir, "dag.log"),
		runctx.WithEnvVars(runenv.EnvKeyExternalStepRetry+"=1"),
	)
	require.NoError(t, node.Prepare(ctx, logDir, "run-1"))

	runner.runNodeExecution(ctx, plan, node, nil)
	require.NoError(t, node.Teardown())

	assert.Equal(t, ir.NodeRetrying, node.State().Status)
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
		step         ir.Step
		dagContainer *ir.Container
	}{
		{
			name: "step env",
			step: ir.Step{
				Name: "render",
				Env:  envs,
			},
		},
		{
			name: "step container env",
			step: ir.Step{
				Name:      "render",
				Container: &ir.Container{Env: envs},
			},
		},
		{
			name: "dag container fallback env",
			step: ir.Step{Name: "render"},
			dagContainer: &ir.Container{
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
				&ir.DAG{
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

func TestPrepareBuildPlanInfersFileDependency(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	runWorkDir := t.TempDir()
	producer := ir.Step{
		ID:      "producer",
		Name:    "producer",
		Outputs: []ir.StepOutputDeclaration{{Name: "artifact", Path: "artifact.txt"}},
	}
	consumer := ir.Step{
		ID:     "consumer",
		Name:   "consumer",
		Inputs: []ir.StepInputDeclaration{{Name: "artifact", Path: "./artifact.txt"}},
	}
	plan, err := NewPlan(producer, consumer)
	require.NoError(t, err)
	ctx := NewContext(context.Background(), &ir.DAG{
		Name:       "build-test",
		Type:       ir.TypeBuild,
		WorkingDir: workingDir,
	}, "run-1", filepath.Join(workingDir, "dag.log"), WithWorkDir(runWorkDir))

	require.NoError(t, prepareBuildPlan(ctx, plan))
	producerNode := plan.GetNodeByName("producer")
	consumerNode := plan.GetNodeByName("consumer")
	require.NotNil(t, producerNode)
	require.NotNil(t, consumerNode)
	assert.True(t, plan.IsInferredDependency(producerNode.ID(), consumerNode.ID()))
	expectedOutput, err := build.ResolvePath(filepath.Join(workingDir, "artifact.txt"), "", true)
	require.NoError(t, err)
	assert.Equal(t, expectedOutput, producerNode.Step().Outputs[0].Path)
	assert.Equal(t, producerNode.Step().Outputs[0].Path, consumerNode.Step().Inputs[0].Path)
}

func TestRunner_StepRetryWithDownstreamIncludesInferredBuildDescendant(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	producer := ir.Step{
		ID:       "producer",
		Name:     "producer",
		Commands: []ir.CommandEntry{{Command: "echo", Args: []string{"producer"}}},
		Outputs:  []ir.StepOutputDeclaration{{Name: "artifact", Path: "artifact.txt"}},
	}
	consumer := ir.Step{
		ID:       "consumer",
		Name:     "consumer",
		Commands: []ir.CommandEntry{{Command: "echo", Args: []string{"consumer"}}},
		Inputs:   []ir.StepInputDeclaration{{Name: "artifact", Path: "artifact.txt"}},
	}
	dag := &ir.DAG{
		Name:               "build-test",
		Type:               ir.TypeBuild,
		WorkingDir:         workingDir,
		WorkingDirExplicit: true,
		Steps:              []ir.Step{producer, consumer},
	}
	nodes := []*Node{
		NodeWithData(NodeData{Step: producer, State: NodeState{Status: ir.NodeSucceeded}}),
		NodeWithData(NodeData{Step: consumer, State: NodeState{Status: ir.NodeSucceeded}}),
	}
	plan, err := CreateStepRetryPlanWithOptions(dag, nodes, producer.Name, StepRetryPlanOptions{
		IncludeDownstream: true,
	})
	require.NoError(t, err)

	runner := New(&Config{
		DAGRunID:             "run-1",
		Dry:                  true,
		NoReuse:              true,
		MaterializationStore: filematerialization.New(filepath.Join(t.TempDir(), "materializations")),
	})
	ctx := NewContext(
		context.Background(),
		dag,
		"run-1",
		filepath.Join(workingDir, "dag.log"),
		WithAttemptID("attempt-1"),
	)
	require.NoError(t, runner.Run(ctx, plan, nil))

	consumerBuild := plan.GetNodeByName(consumer.Name).State().Build
	require.NotNil(t, consumerBuild)
	assert.Equal(t, ir.BuildDecisionDeferred, consumerBuild.Decision)
}

func TestPrepareBuildPlanRejectsRedirectAlias(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		field      string
		targetKind string
		artifact   bool
	}{
		{name: "stdout output", field: "stdout", targetKind: "output"},
		{name: "stderr output", field: "stderr", targetKind: "output"},
		{name: "stdout input", field: "stdout", targetKind: "input"},
		{name: "stderr input", field: "stderr", targetKind: "input"},
		{name: "stdout artifact output", field: "stdout.artifact", targetKind: "output", artifact: true},
		{name: "stderr artifact output", field: "stderr.artifact", targetKind: "output", artifact: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workingDir := t.TempDir()
			runWorkDir := t.TempDir()
			artifactDir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(workingDir, "source.txt"), []byte("source"), 0o600))

			outputPath := "artifact.txt"
			redirectPath := "./source.txt"
			if tt.targetKind == "output" {
				redirectPath = "./artifact.txt"
			}
			if tt.artifact {
				outputPath = "${context.paths.artifacts_dir}/artifact.txt"
				redirectPath = "artifact.txt"
			}
			step := ir.Step{
				ID:      "build",
				Name:    "build",
				Inputs:  []ir.StepInputDeclaration{{Name: "source", Path: "source.txt"}},
				Outputs: []ir.StepOutputDeclaration{{Name: "artifact", Path: outputPath}},
			}
			switch tt.field {
			case "stdout":
				step.Stdout = redirectPath
			case "stderr":
				step.Stderr = redirectPath
			case "stdout.artifact":
				step.StdoutArtifact = redirectPath
			case "stderr.artifact":
				step.StderrArtifact = redirectPath
			}
			plan, err := NewPlan(step)
			require.NoError(t, err)
			ctx := NewContext(context.Background(), &ir.DAG{
				Name:       "build-test",
				Type:       ir.TypeBuild,
				WorkingDir: workingDir,
			}, "run-1", filepath.Join(workingDir, "dag.log"), WithArtifactDir(artifactDir), WithWorkDir(runWorkDir))

			err = prepareBuildPlan(ctx, plan)
			require.ErrorContains(t, err, tt.field+" path aliases build "+tt.targetKind)
		})
	}
}

func TestPrepareBuildPlanChecksPlainRedirectWhenArtifactRedirectIsSet(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	artifactDir := t.TempDir()
	step := ir.Step{
		ID:             "build",
		Name:           "build",
		Outputs:        []ir.StepOutputDeclaration{{Name: "artifact", Path: "artifact.txt"}},
		Stdout:         "./artifact.txt",
		StdoutArtifact: "step.log",
	}
	plan, err := NewPlan(step)
	require.NoError(t, err)
	ctx := NewContext(context.Background(), &ir.DAG{
		Name:               "build-test",
		Type:               ir.TypeBuild,
		WorkingDir:         workingDir,
		WorkingDirExplicit: true,
	}, "run-1", filepath.Join(workingDir, "dag.log"), WithArtifactDir(artifactDir))

	err = prepareBuildPlan(ctx, plan)
	require.ErrorContains(t, err, "stdout path aliases build output")
}

func TestValidateBuildRuntimeRedirectAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		field      string
		targetKind string
		artifact   bool
		reference  string
	}{
		{name: "stdout input", field: "stdout", targetKind: "input", reference: "$producer.output"},
		{name: "stdout artifact output", field: "stdout.artifact", targetKind: "output", artifact: true, reference: "${producer.output}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workingDir := t.TempDir()
			artifactDir := t.TempDir()
			sourcePath := filepath.Join(workingDir, "source.txt")
			require.NoError(t, os.WriteFile(sourcePath, []byte("source"), 0o600))

			producer := ir.Step{ID: "producer", Name: "producer", Output: "TARGET"}
			outputPath := "result.txt"
			resolvedRedirect := sourcePath
			consumer := ir.Step{
				ID:      "consumer",
				Name:    "consumer",
				Depends: []string{"producer"},
				Inputs:  []ir.StepInputDeclaration{{Name: "source", Path: "source.txt"}},
			}
			if tt.artifact {
				outputPath = "${context.paths.artifacts_dir}/result.txt"
				resolvedRedirect = "result.txt"
				consumer.StdoutArtifact = tt.reference
			} else {
				consumer.Stdout = tt.reference
			}
			consumer.Outputs = []ir.StepOutputDeclaration{{Name: "result", Path: outputPath}}

			plan, err := NewPlan(producer, consumer)
			require.NoError(t, err)
			ctx := NewContext(context.Background(), &ir.DAG{
				Name:               "build-test",
				Type:               ir.TypeBuild,
				WorkingDir:         workingDir,
				WorkingDirExplicit: true,
			}, "run-1", filepath.Join(workingDir, "dag.log"), WithArtifactDir(artifactDir))
			require.NoError(t, prepareBuildPlan(ctx, plan))

			producerNode := plan.GetNodeByName("producer")
			consumerNode := plan.GetNodeByName("consumer")
			require.NotNil(t, producerNode)
			require.NotNil(t, consumerNode)
			producerNode.setOutputValue(resolvedRedirect)

			runner := New(&Config{})
			ctx, err = runner.setupVariables(ctx, plan, consumerNode)
			require.NoError(t, err)
			err = validateBuildRuntimeRedirectAliases(ctx, plan, consumerNode)
			require.ErrorContains(t, err, tt.field+" path aliases build "+tt.targetKind)
		})
	}
}

func TestEnvironmentWithoutAttemptPathsRecognizesReferenceForms(t *testing.T) {
	t.Parallel()

	values := []string{
		"KEEP=${params.keep}",
		"BRACED_INPUT=${inputs.source}",
		"PLAIN_INPUT=$inputs.source",
		"BRACED_OUTPUT=${outputs.artifact}",
		"PLAIN_OUTPUT=$outputs.artifact",
	}

	assert.Equal(t, []string{"KEEP=${params.keep}"}, environmentWithoutAttemptPaths(values))
	assert.Equal(t, []string{
		"KEEP=${params.keep}",
		"BRACED_INPUT=${inputs.source}",
		"PLAIN_INPUT=$inputs.source",
	}, environmentWithoutAttemptOutputs(values))
}

func TestBuildInputIsAvailableToStepPrecondition(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	inputPath := filepath.Join(workingDir, "source.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("source"), 0o600))
	step := ir.Step{
		ID:            "build",
		Name:          "build",
		Inputs:        []ir.StepInputDeclaration{{Name: "source", Path: inputPath}},
		Outputs:       []ir.StepOutputDeclaration{{Name: "artifact", Path: filepath.Join(workingDir, "artifact.txt")}},
		Env:           []string{"SOURCE=${inputs.source}"},
		Preconditions: []*ir.Condition{{Condition: `test -f "$SOURCE"`}},
	}
	plan, err := NewPlan(step)
	require.NoError(t, err)
	dag := &ir.DAG{
		Name:               "build-test",
		Type:               ir.TypeBuild,
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
	ctx, session, err := runner.startBuildSession(ctx, plan, node)
	require.NoError(t, err)
	require.NotNil(t, session)
	t.Cleanup(func() { require.NoError(t, session.Close("")) })

	assert.Equal(t, inputPath, GetEnv(ctx).Inputs["source"])
	assert.Equal(t, inputPath, AllEnvsMap(ctx)["SOURCE"])
	require.NoError(t, node.evalPreconditions(ctx))
}

func TestBuildRunnerFingerprintsResolvedRecipe(t *testing.T) {
	if gort.GOOS == "windows" {
		t.Skip("uses a POSIX shell script")
	}

	dataDir := t.TempDir()
	inputPath := filepath.Join(dataDir, "source.txt")
	outputPath := filepath.Join(dataDir, "artifact.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("source"), 0o600))
	store := filematerialization.New(filepath.Join(t.TempDir(), "materializations"))

	run := func(runID, version string) ir.BuildExecution {
		t.Helper()
		step := ir.Step{
			ID:       "build",
			Name:     "build",
			Commands: []ir.CommandEntry{{CmdWithArgs: `printf '%s' "${consts.version}" > "${outputs.artifact}"`}},
			Inputs:   []ir.StepInputDeclaration{{Name: "source", Path: inputPath}},
			Outputs:  []ir.StepOutputDeclaration{{Name: "artifact", Path: outputPath}},
		}
		plan, err := NewPlan(step)
		require.NoError(t, err)
		dag := &ir.DAG{
			Name:       "build-test",
			Type:       ir.TypeBuild,
			WorkingDir: dataDir,
			Shell:      "sh",
			Consts:     map[string]any{"version": version},
		}
		ctx := NewContext(
			context.Background(),
			dag,
			runID,
			filepath.Join(t.TempDir(), "dag.log"),
			WithAttemptID(runID+"-attempt"),
			WithWorkDir(t.TempDir()),
		)
		runner := New(&Config{
			DAGRunID:             runID,
			LogDir:               t.TempDir(),
			MaterializationStore: store,
		})
		require.NoError(t, runner.Run(ctx, plan, nil))
		node := plan.GetNodeByName(step.Name)
		require.NotNil(t, node)
		require.NotNil(t, node.State().Build)
		return *node.State().Build
	}

	first := run("run-1", "v1")
	require.Equal(t, ir.BuildDecisionExecute, first.Decision)
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	require.Equal(t, "v1", string(content))

	second := run("run-2", "v1")
	require.Equal(t, ir.BuildDecisionReuse, second.Decision)

	third := run("run-3", "v2")
	require.Equal(t, ir.BuildDecisionExecute, third.Decision)
	require.Equal(t, ir.BuildReasonRecipeChanged, third.Reason)
	content, err = os.ReadFile(outputPath)
	require.NoError(t, err)
	require.Equal(t, "v2", string(content))
}

func TestEvaluateBuildNodeReportsReusePublishFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workingDir := t.TempDir()
	inputPath := filepath.Join(workingDir, "input.txt")
	outputPath := filepath.Join(workingDir, "output.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	dag := &ir.DAG{Name: "build-test", Type: ir.TypeBuild, WorkingDir: workingDir}
	step := ir.Step{
		ID:       "build",
		Name:     "build",
		Commands: []ir.CommandEntry{{Command: "build"}},
		Inputs:   []ir.StepInputDeclaration{{Name: "source", Path: inputPath}},
		Outputs:  []ir.StepOutputDeclaration{{Name: "artifact", Path: outputPath}},
	}
	store := filematerialization.New(filepath.Join(t.TempDir(), "materializations"))
	request := build.PrepareRequest{
		DAG:         dag,
		Step:        step,
		DAGRunID:    "run-1",
		AttemptID:   "attempt-1",
		WorkingDir:  workingDir,
		Shell:       []string{"sh"},
		Environment: map[string]string{},
	}

	first, err := build.Prepare(ctx, store, request)
	require.NoError(t, err)
	require.NoError(t, first.Evaluate(ctx))
	_, stagingPath, err := first.NewAttempt(0)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(stagingPath, []byte("output"), 0o600))
	require.NoError(t, first.Commit(ctx, stagingPath))
	require.NoError(t, first.Close(stagingPath))

	request.DAGRunID = "run-2"
	request.AttemptID = "attempt-2"
	second, err := build.Prepare(ctx, store, request)
	require.NoError(t, err)
	require.NoError(t, second.Evaluate(ctx))
	require.True(t, second.Reused())
	t.Cleanup(func() { require.NoError(t, second.Close("")) })

	plan, err := NewPlan(step)
	require.NoError(t, err)
	node := plan.GetNodeByName(step.Name)
	require.NotNil(t, node)
	node.setStepOutputsValue("{")
	reported := 0
	runner := New(&Config{})

	handled := runner.evaluateBuildNode(ctx, node, second, func() { reported++ })

	require.True(t, handled)
	require.Equal(t, ir.NodeFailed, node.State().Status)
	require.Equal(t, 1, reported)
}

func TestBuildRepeatRemovesUncommittedStagingOutput(t *testing.T) {
	if gort.GOOS == "windows" {
		t.Skip("uses a POSIX shell script")
	}

	workingDir := t.TempDir()
	inputPath := filepath.Join(workingDir, "input.txt")
	outputPath := filepath.Join(workingDir, "artifact.txt")
	counterPath := filepath.Join(workingDir, "counter")
	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	step := ir.Step{
		ID:   "build",
		Name: "build",
		Script: fmt.Sprintf(`
if [ ! -f %q ]; then
  touch %q
  echo partial > "${outputs.artifact}"
  exit 1
fi
echo complete > "${outputs.artifact}"
`, counterPath, counterPath),
		Inputs:  []ir.StepInputDeclaration{{Name: "source", Path: inputPath}},
		Outputs: []ir.StepOutputDeclaration{{Name: "artifact", Path: outputPath}},
		ContinueOn: ir.ContinueOn{
			Failure:     true,
			MarkSuccess: true,
			ExitCode:    []int{1},
		},
		RepeatPolicy: ir.RepeatPolicy{RepeatMode: ir.RepeatModeUntil},
	}
	plan, err := NewPlan(step)
	require.NoError(t, err)
	runner := New(&Config{
		DAGRunID:             "run-1",
		LogDir:               t.TempDir(),
		MaterializationStore: filematerialization.New(filepath.Join(t.TempDir(), "materializations")),
	})
	dag := &ir.DAG{
		Name:               "build-test",
		Type:               ir.TypeBuild,
		WorkingDir:         workingDir,
		WorkingDirExplicit: true,
		Shell:              "sh",
	}
	ctx := NewContext(context.Background(), dag, "run-1", filepath.Join(workingDir, "dag.log"))

	require.NoError(t, runner.Run(ctx, plan, nil))
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	require.Equal(t, "complete\n", string(content))
	stagingFiles, err := filepath.Glob(filepath.Join(workingDir, ".artifact.txt.dagu-*.tmp"))
	require.NoError(t, err)
	require.Empty(t, stagingFiles)
}

func TestPrepareBuildPlanRejectsInferredCycle(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	first := ir.Step{
		ID:      "first",
		Name:    "first",
		Inputs:  []ir.StepInputDeclaration{{Name: "second", Path: "second.txt"}},
		Outputs: []ir.StepOutputDeclaration{{Name: "first", Path: "first.txt"}},
	}
	second := ir.Step{
		ID:      "second",
		Name:    "second",
		Inputs:  []ir.StepInputDeclaration{{Name: "first", Path: "first.txt"}},
		Outputs: []ir.StepOutputDeclaration{{Name: "second", Path: "second.txt"}},
	}
	plan, err := NewPlan(first, second)
	require.NoError(t, err)
	ctx := NewContext(context.Background(), &ir.DAG{
		Name:       "build-test",
		Type:       ir.TypeBuild,
		WorkingDir: workingDir,
	}, "run-1", filepath.Join(workingDir, "dag.log"))

	err = prepareBuildPlan(ctx, plan)
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
