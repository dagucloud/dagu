// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"sort"
	"strings"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/log"
	"github.com/stretchr/testify/require"
)

// TestEvalExecutorConfig_TemplateTreatsOmittedOptionalParamsAsEmpty verifies
// that optional named params are coerced to empty strings for template config
// evaluation instead of being left as unresolved placeholders.
func TestEvalExecutorConfig_TemplateTreatsOmittedOptionalParamsAsEmpty(t *testing.T) {
	t.Parallel()

	ctx := exec.NewContext(
		context.Background(),
		&core.DAG{
			Name: "test-dag",
			ParamDefs: []core.ParamDef{
				{Name: "name", Type: core.ParamDefTypeString, Required: true},
				{Name: "favorite_color", Type: core.ParamDefTypeString},
			},
		},
		"",
		"",
		exec.WithParams([]string{"name=tom"}),
	)
	env := NewEnv(ctx, core.Step{Name: "render"})
	ctx = WithEnv(ctx, env)

	result, err := evalExecutorConfig(ctx, core.Step{
		ExecutorConfig: core.ExecutorConfig{
			Type: "template",
			Config: map[string]any{
				"data": map[string]any{
					"name":           "${name}",
					"favorite_color": "${favorite_color}",
				},
			},
		},
	})
	require.NoError(t, err)

	data, ok := result["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tom", data["name"])
	require.Equal(t, "", data["favorite_color"])
}

// TestEvalExecutorConfig_TemplatePreservesLiteralCodeFencesInData verifies that
// template config data can carry fenced content without backtick substitution
// executing it during evaluator setup.
func TestEvalExecutorConfig_TemplatePreservesLiteralCodeFencesInData(t *testing.T) {
	t.Parallel()

	ctx := exec.NewContext(
		context.Background(),
		&core.DAG{Name: "test-dag"},
		"",
		"",
	)
	env := NewEnv(ctx, core.Step{Name: "render"})
	env.Scope = env.Scope.WithEntries(map[string]string{
		"ISSUE_TEXT": "```yaml\nenv:\n  TEST_FILE: ~/dagu-test.txt\n\nsteps:\n  - command: touch $TEST_FILE\n```",
	}, cmnvalue.EnvSourceStepEnv)
	ctx = WithEnv(ctx, env)

	result, err := evalExecutorConfig(ctx, core.Step{
		ExecutorConfig: core.ExecutorConfig{
			Type: "template",
			Config: map[string]any{
				"data": map[string]any{
					"issue_text": "${ISSUE_TEXT}",
				},
			},
		},
	})
	require.NoError(t, err)

	data, ok := result["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "```yaml\nenv:\n  TEST_FILE: ~/dagu-test.txt\n\nsteps:\n  - command: touch $TEST_FILE\n```", data["issue_text"])
}

func TestEvalExecutorConfig_TemplateReferenceResolvesOnce(t *testing.T) {
	t.Parallel()

	ctx := exec.NewContext(
		context.Background(),
		&core.DAG{Name: "test-dag"},
		"",
		"",
	)
	env := NewEnv(ctx, core.Step{Name: "render"})
	env.Scope = env.Scope.WithEntries(map[string]string{
		"TEMPLATE": "  Hello, {{ .name }}! ${env.NESTED} `command`\n",
		"NESTED":   "must-not-expand",
	}, cmnvalue.EnvSourceStepEnv)
	ctx = WithEnv(ctx, env)

	result, err := evalExecutorConfig(ctx, core.Step{
		ExecutorConfig: core.ExecutorConfig{
			Type: "template",
			Config: map[string]any{
				"template_ref": "${env.TEMPLATE}",
				"data":         map[string]any{"name": "Alice"},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "  Hello, {{ .name }}! ${env.NESTED} `command`\n", result["template_ref"])
	require.Equal(t, map[string]any{"name": "Alice"}, result["data"])
}

func TestEvalExecutorConfig_TemplateReferenceMustResolve(t *testing.T) {
	t.Parallel()

	ctx := exec.NewContext(
		context.Background(),
		&core.DAG{Name: "test-dag"},
		"",
		"",
	)
	env := NewEnv(ctx, core.Step{Name: "render"})
	ctx = WithEnv(ctx, env)

	_, err := evalExecutorConfig(ctx, core.Step{
		ExecutorConfig: core.ExecutorConfig{
			Type: "template",
			Config: map[string]any{
				"template_ref": "${env.MISSING}",
			},
		},
	})
	require.ErrorContains(t, err, "unknown env.MISSING binding")
}

// TestEvalExecutorConfig_DefaultPreservesLiteralCodeFencesInData verifies that
// non-template executor config is also treated as step data and should not
// execute backticks while resolving variable references.
func TestEvalExecutorConfig_DefaultPreservesLiteralCodeFencesInData(t *testing.T) {
	t.Parallel()

	ctx := exec.NewContext(
		context.Background(),
		&core.DAG{Name: "test-dag"},
		"",
		"",
	)
	env := NewEnv(ctx, core.Step{Name: "analyze"})
	env.Scope = env.Scope.WithEntries(map[string]string{
		"PROMPT_TEXT": "```yaml\nenv:\n  TEST_FILE: ~/dagu-test.txt\n\nsteps:\n  - command: touch $TEST_FILE\n```",
	}, cmnvalue.EnvSourceStepEnv)
	ctx = WithEnv(ctx, env)

	result, err := evalExecutorConfig(ctx, core.Step{
		ExecutorConfig: core.ExecutorConfig{
			Type: "harness",
			Config: map[string]any{
				"provider": "codex",
				"note":     "${PROMPT_TEXT}",
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "```yaml\nenv:\n  TEST_FILE: ~/dagu-test.txt\n\nsteps:\n  - command: touch $TEST_FILE\n```", result["note"])
}

func TestEvalExecutorConfig_ExpandsStepOutputsReferences(t *testing.T) {
	t.Parallel()

	ctx := exec.NewContext(
		context.Background(),
		&core.DAG{Name: "test-dag"},
		"",
		"",
	)
	env := NewEnv(ctx, core.Step{Name: "audit"})
	outputs := `{"messageId":"msg-123","status":"sent"}`
	env.StepMap["call_action"] = cmnvalue.StepInfo{Outputs: &outputs}
	ctx = WithEnv(ctx, env)

	result, err := evalExecutorConfig(ctx, core.Step{
		ExecutorConfig: core.ExecutorConfig{
			Type: "log",
			Config: map[string]any{
				"message": "message=${call_action.outputs.messageId} status=${call_action.outputs.status}",
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "message=msg-123 status=sent", result["message"])
}

func TestSetupExecutor_LogMessageExpandsVariables(t *testing.T) {
	t.Parallel()

	step := core.Step{
		Name: "announce",
		ExecutorConfig: core.ExecutorConfig{
			Type: "log",
			Config: map[string]any{
				"message": "Deploying ${ENVIRONMENT}",
			},
		},
	}
	ctx := NewContextForTest(context.Background(), &core.DAG{Name: "test-dag"}, "run-1", "test.log")
	env := NewEnv(ctx, step)
	env.Scope = env.Scope.WithEntries(map[string]string{
		"ENVIRONMENT": "production",
	}, cmnvalue.EnvSourceStepEnv)
	ctx = WithEnv(ctx, env)

	node := NewNode(step, NodeState{})
	runCtx, cmd, err := node.setupExecutor(ctx)
	require.NoError(t, err)

	var stdout strings.Builder
	cmd.SetStdout(&stdout)
	require.NoError(t, cmd.Run(runCtx))
	require.Equal(t, "Deploying production\n", stdout.String())
}

// TestBuildSubDAGRunsAddressesPreviousAttemptRuns verifies that a manual step
// retry can address the child DAG runs of the previous attempt: the rebuilt sub
// run IDs match the ones the first attempt produced, even though the retry
// starts the step from a cleared state.
func TestBuildSubDAGRunsAddressesPreviousAttemptRuns(t *testing.T) {
	t.Parallel()

	step := core.Step{
		Name:   "parallel_2",
		SubDAG: &core.SubDAG{Name: "child"},
		Parallel: &core.ParallelConfig{
			Items: []core.ParallelItem{{Value: "one"}, {Value: "two"}},
		},
	}
	dag := &core.DAG{Name: "root", Steps: []core.Step{step}}

	buildIDs := func(t *testing.T, node *Node) []string {
		t.Helper()
		ctx := NewContextForTest(context.Background(), dag, "root-run", "")
		ctx = WithEnv(ctx, NewEnv(ctx, step))
		runs, err := node.BuildSubDAGRuns(ctx, step.SubDAG)
		require.NoError(t, err)
		ids := make([]string, 0, len(runs))
		for _, run := range runs {
			ids = append(ids, run.DAGRunID)
		}
		sort.Strings(ids)
		return ids
	}

	firstAttempt := buildIDs(t, NewNode(step, NodeState{}))
	require.Len(t, firstAttempt, 2)

	retried := NewNode(step, NodeState{
		Status:  core.NodeFailed,
		SubRuns: []SubDAGRun{{DAGRunID: firstAttempt[0]}, {DAGRunID: firstAttempt[1]}},
	})
	_, err := CreateStepRetryPlan(dag, []*Node{retried}, step.Name)
	require.NoError(t, err)

	require.Equal(t, firstAttempt, buildIDs(t, retried))
}

// TestSetupExecutor_HarnessCommandPreservesLiteralCodeFences verifies that
// command-backed prompt executors resolve ${VAR} placeholders without treating
// the resulting prompt text as shell command substitution input.
func TestSetupExecutor_HarnessCommandPreservesLiteralCodeFences(t *testing.T) {
	t.Parallel()

	step := core.Step{
		Name: "analyze",
		ExecutorConfig: core.ExecutorConfig{
			Type:   "harness",
			Config: map[string]any{"provider": "codex"},
		},
		Commands: []core.CommandEntry{{
			CmdWithArgs: "${ANALYZE_PROMPT}",
		}},
	}
	ctx := NewContextForTest(context.Background(), &core.DAG{Name: "test-dag"}, "run-1", "test.log")
	env := NewEnv(ctx, step)
	env.Scope = env.Scope.WithEntries(map[string]string{
		"ANALYZE_PROMPT": "```yaml\nenv:\n  TEST_FILE: ~/dagu-test.txt\n\nsteps:\n  - command: touch $TEST_FILE\n```",
	}, cmnvalue.EnvSourceStepEnv)
	ctx = WithEnv(ctx, env)

	node := NewNode(step, NodeState{})
	_, _, err := node.setupExecutor(ctx)
	require.NoError(t, err)
	require.Equal(t, "```yaml\nenv:\n  TEST_FILE: ~/dagu-test.txt\n\nsteps:\n  - command: touch $TEST_FILE\n```", node.Step().Commands[0].CmdWithArgs)
}

// TestSetupExecutor_HarnessScriptPreservesLiteralCodeFences verifies that
// script-backed prompt content is preserved literally until the target executor
// consumes it.
func TestSetupExecutor_HarnessScriptPreservesLiteralCodeFences(t *testing.T) {
	t.Parallel()

	step := core.Step{
		Name: "analyze",
		ExecutorConfig: core.ExecutorConfig{
			Type:   "harness",
			Config: map[string]any{"provider": "codex"},
		},
		Commands: []core.CommandEntry{{
			CmdWithArgs: "Summarize the issue",
		}},
		Script: "${ANALYZE_SCRIPT}",
	}
	ctx := NewContextForTest(context.Background(), &core.DAG{Name: "test-dag"}, "run-1", "test.log")
	env := NewEnv(ctx, step)
	env.Scope = env.Scope.WithEntries(map[string]string{
		"ANALYZE_SCRIPT": "```yaml\nenv:\n  TEST_FILE: ~/dagu-test.txt\n\nsteps:\n  - command: touch $TEST_FILE\n```",
	}, cmnvalue.EnvSourceStepEnv)
	ctx = WithEnv(ctx, env)

	node := NewNode(step, NodeState{})
	_, _, err := node.setupExecutor(ctx)
	require.NoError(t, err)
	require.Equal(t, "```yaml\nenv:\n  TEST_FILE: ~/dagu-test.txt\n\nsteps:\n  - command: touch $TEST_FILE\n```", node.Step().Script)
}
