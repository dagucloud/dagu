// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/stretchr/testify/require"
)

type declaredOutputExecutor struct {
	run    func(context.Context, *declaredOutputExecutor) error
	stdout io.Writer
	stderr io.Writer
}

func (e *declaredOutputExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *declaredOutputExecutor) SetStderr(out io.Writer) { e.stderr = out }
func (e *declaredOutputExecutor) Kill(os.Signal) error    { return nil }
func (e *declaredOutputExecutor) Run(ctx context.Context) error {
	if e.run == nil {
		return nil
	}
	return e.run(ctx, e)
}

func TestStepExecutorPublishesDeclaredStepOutputs(t *testing.T) {
	executorType := registerDeclaredOutputExecutor(t, func(ctx context.Context, _ *declaredOutputExecutor) error {
		path := outputFilePathFromContext(t, ctx)
		return os.WriteFile(
			path,
			[]byte("image_tag=v1.2.3\nmetadata<<JSON\n{\"image\":\"api\",\"tag\":\"v1.2.3\"}\nJSON\n"),
			0o600,
		)
	})

	node := newDeclaredOutputNode(t, executorType, []core.StepOutputDeclaration{
		{Name: "image_tag", Type: core.StepDeclaredOutputTypeString},
		{Name: "metadata", Type: core.StepDeclaredOutputTypeJSON},
	})
	ctx := runtime.NewContext(context.Background(), &core.DAG{}, "run-1", "dag.log")

	require.NoError(t, runtime.NewStepExecutor().Execute(ctx, node))
	state := node.State()
	require.NotNil(t, state.StepOutputsValue)
	require.JSONEq(t, `{"image_tag":"v1.2.3","metadata":"{\"image\":\"api\",\"tag\":\"v1.2.3\"}"}`, *state.StepOutputsValue)
	require.Nil(t, state.OutputsValue)

	info := node.StepInfo()
	require.NotNil(t, info.Outputs)
	require.JSONEq(t, *state.StepOutputsValue, *info.Outputs)
}

func TestStepInfoFallsBackToLegacyOutputsValue(t *testing.T) {
	legacyOutputs := `{"messageId":"msg-123","worker":"shared-volume"}`
	node := runtime.NewNode(core.Step{Name: "call_action", ID: "call_action"}, runtime.NodeState{
		OutputsValue: &legacyOutputs,
	})

	info := node.StepInfo()
	require.NotNil(t, info.Outputs)
	require.JSONEq(t, legacyOutputs, *info.Outputs)
}

func TestStepExecutorDoesNotReadStdoutAsDeclaredStepOutput(t *testing.T) {
	executorType := registerDeclaredOutputExecutor(t, func(_ context.Context, e *declaredOutputExecutor) error {
		_, err := fmt.Fprintln(e.stdout, "image_tag=v1.2.3")
		return err
	})

	node := newDeclaredOutputNode(t, executorType, []core.StepOutputDeclaration{
		{Name: "image_tag", Type: core.StepDeclaredOutputTypeString},
	})
	ctx := runtime.NewContext(context.Background(), &core.DAG{}, "run-1", "dag.log")

	err := runtime.NewStepExecutor().Execute(ctx, node)
	require.Error(t, err)
	require.Contains(t, err.Error(), `declared step output "image_tag" was not emitted`)
	require.Nil(t, node.State().StepOutputsValue)
}

func TestStepExecutorFailsOnUndeclaredStepOutputWrite(t *testing.T) {
	executorType := registerDeclaredOutputExecutor(t, func(ctx context.Context, _ *declaredOutputExecutor) error {
		return os.WriteFile(outputFilePathFromContext(t, ctx), []byte("unexpected=value\n"), 0o600)
	})

	node := newDeclaredOutputNode(t, executorType, nil)
	ctx := runtime.NewContext(context.Background(), &core.DAG{}, "run-1", "dag.log")

	err := runtime.NewStepExecutor().Execute(ctx, node)
	require.Error(t, err)
	require.Contains(t, err.Error(), `undeclared step output "unexpected"`)
	require.Nil(t, node.State().StepOutputsValue)
}

func TestStepExecutorFailsOnInvalidJSONStepOutput(t *testing.T) {
	executorType := registerDeclaredOutputExecutor(t, func(ctx context.Context, _ *declaredOutputExecutor) error {
		return os.WriteFile(outputFilePathFromContext(t, ctx), []byte("metadata={bad json}\n"), 0o600)
	})

	node := newDeclaredOutputNode(t, executorType, []core.StepOutputDeclaration{
		{Name: "metadata", Type: core.StepDeclaredOutputTypeJSON},
	})
	ctx := runtime.NewContext(context.Background(), &core.DAG{}, "run-1", "dag.log")

	err := runtime.NewStepExecutor().Execute(ctx, node)
	require.Error(t, err)
	require.Contains(t, err.Error(), `step output "metadata" is not valid JSON`)
	require.Nil(t, node.State().StepOutputsValue)
}

func TestStepExecutorDoesNotPublishDeclaredOutputsFromFailedAttempt(t *testing.T) {
	executorType := registerDeclaredOutputExecutor(t, func(ctx context.Context, _ *declaredOutputExecutor) error {
		if err := os.WriteFile(outputFilePathFromContext(t, ctx), []byte("image_tag=v1.2.3\n"), 0o600); err != nil {
			return err
		}
		return errors.New("executor failed")
	})

	node := newDeclaredOutputNode(t, executorType, []core.StepOutputDeclaration{
		{Name: "image_tag", Type: core.StepDeclaredOutputTypeString},
	})
	ctx := runtime.NewContext(context.Background(), &core.DAG{}, "run-1", "dag.log")

	err := runtime.NewStepExecutor().Execute(ctx, node)
	require.Error(t, err)
	require.Contains(t, err.Error(), "executor failed")
	require.Nil(t, node.State().StepOutputsValue)
}

func TestNewPlanEnvForNodeIncludesOnlyPredecessorStepReferences(t *testing.T) {
	t.Parallel()

	plan, err := runtime.NewPlan(
		core.Step{Name: "build", ID: "build"},
		core.Step{Name: "sibling", ID: "sibling"},
		core.Step{Name: "deploy", ID: "deploy", Depends: []string{"build"}},
	)
	require.NoError(t, err)
	deploy := plan.GetNodeByName("deploy")
	require.NotNil(t, deploy)

	env := runtime.NewPlanEnvForNode(context.Background(), deploy, plan)
	require.Contains(t, env.StepMap, "build")
	require.NotContains(t, env.StepMap, "sibling")
	require.NotContains(t, env.StepMap, "deploy")
}

func registerDeclaredOutputExecutor(t *testing.T, run func(context.Context, *declaredOutputExecutor) error) string {
	t.Helper()

	executorType := "test-declared-step-output-" + t.Name()
	runtimeexec.RegisterExecutor(executorType, func(context.Context, core.Step) (runtimeexec.Executor, error) {
		return &declaredOutputExecutor{run: run}, nil
	}, nil, core.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })
	return executorType
}

func outputFilePathFromContext(t *testing.T, ctx context.Context) string {
	t.Helper()

	value, ok := runtime.GetEnv(ctx).Scope.Get(exec.EnvKeyDAGUOutputFile)
	require.True(t, ok)
	require.NotEmpty(t, value)
	return value
}

func newDeclaredOutputNode(t *testing.T, executorType string, outputs []core.StepOutputDeclaration) *runtime.Node {
	t.Helper()

	logDir := t.TempDir()
	return runtime.NewNode(core.Step{
		Name: "publish",
		ID:   "publish",
		ExecutorConfig: core.ExecutorConfig{
			Type: executorType,
		},
		Outputs: outputs,
	}, runtime.NodeState{
		Stdout: filepath.Join(logDir, "publish.out"),
		Stderr: filepath.Join(logDir, "publish.err"),
	})
}
