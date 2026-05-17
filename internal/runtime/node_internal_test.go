// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/eval"
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
	}, eval.EnvSourceStepEnv)
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
	}, eval.EnvSourceStepEnv)
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
	env.StepMap["call_action"] = eval.StepInfo{Outputs: &outputs}
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
	}, eval.EnvSourceStepEnv)
	ctx = WithEnv(ctx, env)

	node := NewNode(step, NodeState{})
	cmd, err := node.setupExecutor(ctx)
	require.NoError(t, err)

	var stdout strings.Builder
	cmd.SetStdout(&stdout)
	require.NoError(t, cmd.Run(ctx))
	require.Equal(t, "Deploying production\n", stdout.String())
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
	}, eval.EnvSourceStepEnv)
	ctx = WithEnv(ctx, env)

	node := NewNode(step, NodeState{})
	_, err := node.setupExecutor(ctx)
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
	}, eval.EnvSourceStepEnv)
	ctx = WithEnv(ctx, env)

	node := NewNode(step, NodeState{})
	_, err := node.setupExecutor(ctx)
	require.NoError(t, err)
	require.Equal(t, "```yaml\nenv:\n  TEST_FILE: ~/dagu-test.txt\n\nsteps:\n  - command: touch $TEST_FILE\n```", node.Step().Script)
}
