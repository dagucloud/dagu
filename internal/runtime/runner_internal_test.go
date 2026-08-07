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

func TestPrepareIncrementalPlanRejectsRedirectAlias(t *testing.T) {
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
			step := core.Step{
				ID:      "build",
				Name:    "build",
				Inputs:  []core.StepInputDeclaration{{Name: "source", Path: "source.txt"}},
				Outputs: []core.StepOutputDeclaration{{Name: "artifact", Path: outputPath}},
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
			ctx := NewContext(context.Background(), &core.DAG{
				Name:       "incremental-test",
				Type:       core.TypeIncremental,
				WorkingDir: workingDir,
			}, "run-1", filepath.Join(workingDir, "dag.log"), WithArtifactDir(artifactDir), WithWorkDir(runWorkDir))

			err = prepareIncrementalPlan(ctx, plan)
			require.ErrorContains(t, err, tt.field+" path aliases incremental "+tt.targetKind)
		})
	}
}

func TestPrepareIncrementalPlanChecksPlainRedirectWhenArtifactRedirectIsSet(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	artifactDir := t.TempDir()
	step := core.Step{
		ID:             "build",
		Name:           "build",
		Outputs:        []core.StepOutputDeclaration{{Name: "artifact", Path: "artifact.txt"}},
		Stdout:         "./artifact.txt",
		StdoutArtifact: "step.log",
	}
	plan, err := NewPlan(step)
	require.NoError(t, err)
	ctx := NewContext(context.Background(), &core.DAG{
		Name:               "incremental-test",
		Type:               core.TypeIncremental,
		WorkingDir:         workingDir,
		WorkingDirExplicit: true,
	}, "run-1", filepath.Join(workingDir, "dag.log"), WithArtifactDir(artifactDir))

	err = prepareIncrementalPlan(ctx, plan)
	require.ErrorContains(t, err, "stdout path aliases incremental output")
}

func TestValidateIncrementalRuntimeRedirectAliases(t *testing.T) {
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

			producer := core.Step{ID: "producer", Name: "producer", Output: "TARGET"}
			outputPath := "result.txt"
			resolvedRedirect := sourcePath
			consumer := core.Step{
				ID:      "consumer",
				Name:    "consumer",
				Depends: []string{"producer"},
				Inputs:  []core.StepInputDeclaration{{Name: "source", Path: "source.txt"}},
			}
			if tt.artifact {
				outputPath = "${context.paths.artifacts_dir}/result.txt"
				resolvedRedirect = "result.txt"
				consumer.StdoutArtifact = tt.reference
			} else {
				consumer.Stdout = tt.reference
			}
			consumer.Outputs = []core.StepOutputDeclaration{{Name: "result", Path: outputPath}}

			plan, err := NewPlan(producer, consumer)
			require.NoError(t, err)
			ctx := NewContext(context.Background(), &core.DAG{
				Name:               "incremental-test",
				Type:               core.TypeIncremental,
				WorkingDir:         workingDir,
				WorkingDirExplicit: true,
			}, "run-1", filepath.Join(workingDir, "dag.log"), WithArtifactDir(artifactDir))
			require.NoError(t, prepareIncrementalPlan(ctx, plan))

			producerNode := plan.GetNodeByName("producer")
			consumerNode := plan.GetNodeByName("consumer")
			require.NotNil(t, producerNode)
			require.NotNil(t, consumerNode)
			producerNode.setOutputValue(resolvedRedirect)

			runner := New(&Config{})
			ctx, err = runner.setupVariables(ctx, plan, consumerNode)
			require.NoError(t, err)
			err = validateIncrementalRuntimeRedirectAliases(ctx, plan, consumerNode)
			require.ErrorContains(t, err, tt.field+" path aliases incremental "+tt.targetKind)
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
		Env:           []string{"SOURCE=${inputs.source}"},
		Preconditions: []*core.Condition{{Condition: `test -f "$SOURCE"`}},
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
	assert.Equal(t, inputPath, AllEnvsMap(ctx)["SOURCE"])
	require.NoError(t, node.evalPreconditions(ctx))
}

func TestIncrementalRunnerFingerprintsResolvedRecipe(t *testing.T) {
	if gort.GOOS == "windows" {
		t.Skip("uses a POSIX shell script")
	}

	dataDir := t.TempDir()
	inputPath := filepath.Join(dataDir, "source.txt")
	outputPath := filepath.Join(dataDir, "artifact.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("source"), 0o600))
	store := filematerialization.New(filepath.Join(t.TempDir(), "materializations"))

	run := func(runID, version string) exec.IncrementalExecution {
		t.Helper()
		step := core.Step{
			ID:       "build",
			Name:     "build",
			Commands: []core.CommandEntry{{CmdWithArgs: `printf '%s' "${consts.version}" > "${outputs.artifact}"`}},
			Inputs:   []core.StepInputDeclaration{{Name: "source", Path: inputPath}},
			Outputs:  []core.StepOutputDeclaration{{Name: "artifact", Path: outputPath}},
		}
		plan, err := NewPlan(step)
		require.NoError(t, err)
		dag := &core.DAG{
			Name:       "incremental-test",
			Type:       core.TypeIncremental,
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
		require.NotNil(t, node.State().Incremental)
		return *node.State().Incremental
	}

	first := run("run-1", "v1")
	require.Equal(t, exec.IncrementalDecisionExecute, first.Decision)
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	require.Equal(t, "v1", string(content))

	second := run("run-2", "v1")
	require.Equal(t, exec.IncrementalDecisionReuse, second.Decision)

	third := run("run-3", "v2")
	require.Equal(t, exec.IncrementalDecisionExecute, third.Decision)
	require.Equal(t, exec.IncrementalReasonRecipeChanged, third.Reason)
	content, err = os.ReadFile(outputPath)
	require.NoError(t, err)
	require.Equal(t, "v2", string(content))
}

func TestEvaluateIncrementalNodeReportsReusePublishFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workingDir := t.TempDir()
	inputPath := filepath.Join(workingDir, "input.txt")
	outputPath := filepath.Join(workingDir, "output.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	dag := &core.DAG{Name: "incremental-test", Type: core.TypeIncremental, WorkingDir: workingDir}
	step := core.Step{
		ID:       "build",
		Name:     "build",
		Commands: []core.CommandEntry{{Command: "build"}},
		Inputs:   []core.StepInputDeclaration{{Name: "source", Path: inputPath}},
		Outputs:  []core.StepOutputDeclaration{{Name: "artifact", Path: outputPath}},
	}
	store := filematerialization.New(filepath.Join(t.TempDir(), "materializations"))
	request := incremental.PrepareRequest{
		DAG:         dag,
		Step:        step,
		DAGRunID:    "run-1",
		AttemptID:   "attempt-1",
		WorkingDir:  workingDir,
		Shell:       []string{"sh"},
		Environment: map[string]string{},
	}

	first, err := incremental.Prepare(ctx, store, request)
	require.NoError(t, err)
	require.NoError(t, first.Evaluate(ctx))
	_, stagingPath, err := first.NewAttempt(0)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(stagingPath, []byte("output"), 0o600))
	require.NoError(t, first.Commit(ctx, stagingPath))
	require.NoError(t, first.Close(stagingPath))

	request.DAGRunID = "run-2"
	request.AttemptID = "attempt-2"
	second, err := incremental.Prepare(ctx, store, request)
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

	handled := runner.evaluateIncrementalNode(ctx, node, second, func() { reported++ })

	require.True(t, handled)
	require.Equal(t, core.NodeFailed, node.State().Status)
	require.Equal(t, 1, reported)
}

func TestIncrementalRepeatRemovesUncommittedStagingOutput(t *testing.T) {
	if gort.GOOS == "windows" {
		t.Skip("uses a POSIX shell script")
	}

	workingDir := t.TempDir()
	inputPath := filepath.Join(workingDir, "input.txt")
	outputPath := filepath.Join(workingDir, "artifact.txt")
	counterPath := filepath.Join(workingDir, "counter")
	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	step := core.Step{
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
		Inputs:  []core.StepInputDeclaration{{Name: "source", Path: inputPath}},
		Outputs: []core.StepOutputDeclaration{{Name: "artifact", Path: outputPath}},
		ContinueOn: core.ContinueOn{
			Failure:     true,
			MarkSuccess: true,
			ExitCode:    []int{1},
		},
		RepeatPolicy: core.RepeatPolicy{RepeatMode: core.RepeatModeUntil},
	}
	plan, err := NewPlan(step)
	require.NoError(t, err)
	runner := New(&Config{
		DAGRunID:             "run-1",
		LogDir:               t.TempDir(),
		MaterializationStore: filematerialization.New(filepath.Join(t.TempDir(), "materializations")),
	})
	dag := &core.DAG{
		Name:               "incremental-test",
		Type:               core.TypeIncremental,
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
