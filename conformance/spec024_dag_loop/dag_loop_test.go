// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec024_dag_loop_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestValidateDAGLoop(t *testing.T) {
	t.Parallel()

	validCases := []string{
		"valid_until_shortcut.yaml",
		"valid_while_conditions.yaml",
		"valid_exactly_100_conditions.yaml",
		"valid_bounds_and_defaults.yaml",
		"valid_backoff_true.yaml",
		"valid_backoff_number.yaml",
		"valid_backoff_false.yaml",
		"valid_value_resolved_bounds.yaml",
		"validate_does_not_execute.yaml",
	}
	for _, file := range validCases {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderr("")
			dagu.ExpectNoFile("validate-loop-ran.txt")
		})
	}

	invalidCases := []struct {
		name string
		file string
	}{
		{name: "loop must be an object", file: "invalid_loop_scalar.yaml"},
		{name: "unknown loop field", file: "invalid_unknown_field.yaml"},
		{name: "missing loop mode", file: "invalid_missing_mode.yaml"},
		{name: "both loop modes", file: "invalid_both_modes.yaml"},
		{name: "empty mode string", file: "invalid_empty_string.yaml"},
		{name: "non-string mode scalar", file: "invalid_mode_number.yaml"},
		{name: "empty condition list", file: "invalid_empty_list.yaml"},
		{name: "too many conditions", file: "invalid_too_many_conditions.yaml"},
		{name: "scalar condition entry", file: "invalid_condition_scalar.yaml"},
		{name: "empty condition text", file: "invalid_condition_empty.yaml"},
		{name: "condition and eval", file: "invalid_condition_and_eval.yaml"},
		{name: "empty eval text", file: "invalid_eval_empty.yaml"},
		{name: "eval without expected", file: "invalid_eval_without_expected.yaml"},
		{name: "non-string expected", file: "invalid_expected_non_string.yaml"},
		{name: "non-boolean negate", file: "invalid_negate_non_bool.yaml"},
		{name: "invalid regex", file: "invalid_condition_regex.yaml"},
		{name: "unknown condition field", file: "invalid_condition_unknown_field.yaml"},
		{name: "missing max iterations", file: "invalid_missing_max_iterations.yaml"},
		{name: "zero max iterations", file: "invalid_max_iterations_zero.yaml"},
		{name: "max iterations above bound", file: "invalid_max_iterations_too_high.yaml"},
		{name: "fractional max iterations", file: "invalid_max_iterations_fraction.yaml"},
		{name: "negative interval", file: "invalid_interval_negative.yaml"},
		{name: "interval above bound", file: "invalid_interval_too_high.yaml"},
		{name: "fractional interval", file: "invalid_interval_fraction.yaml"},
		{name: "backoff string", file: "invalid_backoff_string.yaml"},
		{name: "backoff not greater than one", file: "invalid_backoff_one.yaml"},
		{name: "max interval without backoff", file: "invalid_max_interval_without_backoff.yaml"},
		{name: "max interval with disabled backoff", file: "invalid_max_interval_with_false_backoff.yaml"},
		{name: "enabled backoff missing max interval", file: "invalid_backoff_missing_max_interval.yaml"},
		{name: "zero max interval", file: "invalid_max_interval_zero.yaml"},
		{name: "max interval above bound", file: "invalid_max_interval_too_high.yaml"},
		{name: "fractional max interval", file: "invalid_max_interval_fraction.yaml"},
		{name: "invalid exhaustion policy", file: "invalid_on_exhausted.yaml"},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStdout("")
			result.ExpectStderrContains("loop")
		})
	}
}

func TestRuntimeLoopModesAndStepResetUnix(t *testing.T) {
	t.Parallel()
	skipPOSIXFixtures(t)

	cases := []struct {
		name  string
		file  string
		files map[string]string
	}{
		{
			name: "until repeats until the command check passes",
			file: "until_command.yaml",
			files: map[string]string{
				"until-count.txt": "x\nx\nx\n",
			},
		},
		{
			name: "while always executes its body before checking",
			file: "while_do_while.yaml",
			files: map[string]string{
				"while-count.txt": "x\n",
			},
		},
		{
			name: "while repeats while its condition list passes",
			file: "while_continues.yaml",
			files: map[string]string{
				"while-continues-count.txt": "x\nx\nx\n",
			},
		},
		{
			name: "normal completion does not wait an interval",
			file: "no_interval_after_normal_end.yaml",
			files: map[string]string{
				"no-final-wait.txt": "x\n",
			},
		},
		{
			name: "value match sees the current iteration output",
			file: "until_step_output.yaml",
			files: map[string]string{
				"output-attempts.txt": "x\nx\n",
			},
		},
		{
			name: "chain body resets all steps",
			file: "chain_body_reset.yaml",
			files: map[string]string{
				"chain-order.txt": "a-1\nb-1\na-2\nb-2\n",
			},
		},
		{
			name: "graph body resets independent steps",
			file: "graph_body_reset.yaml",
			files: map[string]string{
				"graph-a.txt": "1\n2\n",
				"graph-b.txt": "1\n2\n",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			for file, content := range tc.files {
				dagu.ExpectFileContent(file, content)
			}
		})
	}
}

func TestRuntimeLoopLifecycleAndTerminalStatusUnix(t *testing.T) {
	t.Parallel()
	skipPOSIXFixtures(t)

	cases := []struct {
		name        string
		file        string
		exitCode    int
		files       map[string]string
		absentFiles []string
	}{
		{
			name:     "root precondition aborts before init and first iteration",
			file:     "root_precondition_aborts.yaml",
			exitCode: 0,
			files: map[string]string{
				"abort-handler.txt": "abort\n",
			},
			absentFiles: []string{"init-ran.txt", "loop-step-ran.txt", "loop-condition-ran.txt"},
		},
		{
			name:     "root and terminal lifecycle run once",
			file:     "lifecycle_once.yaml",
			exitCode: 0,
			files: map[string]string{
				"lifecycle.txt": "root-pre\ninit\nstep-pre-1\nstep-1\ncondition-1\nstep-pre-2\nstep-2\ncondition-2\nsuccess\nexit\n",
			},
			absentFiles: []string{"failure-handler.txt"},
		},
		{
			name:     "default exhaustion fails and selects failure handler",
			file:     "exhaustion_fail.yaml",
			exitCode: 1,
			files: map[string]string{
				"exhaustion-fail-count.txt": "x\nx\n",
				"failure-handler.txt":       "failure\n",
				"exit-handler.txt":          "exit\n",
			},
			absentFiles: []string{"success-handler.txt"},
		},
		{
			name:     "successful exhaustion preserves final iteration status",
			file:     "exhaustion_succeed.yaml",
			exitCode: 0,
			files: map[string]string{
				"exhaustion-succeed-count.txt": "x\nx\n",
				"success-handler.txt":          "success\n",
				"exit-handler.txt":             "exit\n",
			},
			absentFiles: []string{"failure-handler.txt"},
		},
		{
			name:     "normal completion on final allowed iteration is not exhaustion",
			file:     "final_iteration_normal.yaml",
			exitCode: 0,
			files: map[string]string{
				"final-normal-count.txt": "x\nx\n",
			},
		},
		{
			name:     "failed iteration stops before condition evaluation",
			file:     "failed_iteration.yaml",
			exitCode: 1,
			files: map[string]string{
				"failed-iteration-count.txt": "x\n",
				"failure-handler.txt":        "failure\n",
				"exit-handler.txt":           "exit\n",
			},
			absentFiles: []string{"condition-ran.txt", "success-handler.txt"},
		},
		{
			name:     "partially succeeded iteration evaluates the condition",
			file:     "partial_iteration.yaml",
			exitCode: 0,
			files: map[string]string{
				"partial-attempts.txt":   "x\nx\n",
				"partial-conditions.txt": "1\n2\n",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(tc.exitCode)
			for file, content := range tc.files {
				dagu.ExpectFileContent(file, content)
			}
			for _, file := range tc.absentFiles {
				dagu.ExpectNoFile(file)
			}
		})
	}
}

func TestRuntimeLoopConditionSemanticsUnix(t *testing.T) {
	t.Parallel()
	skipPOSIXFixtures(t)

	cases := []struct {
		name     string
		file     string
		exitCode int
		files    map[string]string
	}{
		{
			name:     "condition checks inherit root shell environment and working directory",
			file:     "condition_root_context.yaml",
			exitCode: 0,
			files: map[string]string{
				"condition-work/shell-invocations.txt": "shell\nshell\n",
			},
		},
		{
			name:     "all entries run in source order",
			file:     "condition_source_order.yaml",
			exitCode: 0,
			files: map[string]string{
				"condition-order.txt": "one\ntwo\none\ntwo\n",
			},
		},
		{
			name:     "negation is applied before the list result",
			file:     "condition_negate.yaml",
			exitCode: 0,
			files: map[string]string{
				"negate-count.txt": "x\n",
			},
		},
		{
			name:     "regex value match controls continuation",
			file:     "condition_regex.yaml",
			exitCode: 0,
			files: map[string]string{
				"regex-count.txt": "x\nx\n",
			},
		},
		{
			name:     "expected value remains literal",
			file:     "condition_expected_literal.yaml",
			exitCode: 0,
			files: map[string]string{
				"expected-literal-count.txt": "x\nx\n",
			},
		},
		{
			name:     "missing command is a normal not-met result",
			file:     "condition_missing_command.yaml",
			exitCode: 0,
			files: map[string]string{
				"missing-command-count.txt": "x\nx\n",
			},
		},
		{
			name:     "condition process start failure is a normal not-met result",
			file:     "condition_process_start_failure.yaml",
			exitCode: 0,
			files: map[string]string{
				"process-start-failure-count.txt": "x\nx\n",
			},
		},
		{
			name:     "dynamic evaluation failure stops the loop",
			file:     "condition_dynamic_evaluation_error.yaml",
			exitCode: 1,
			files: map[string]string{
				"dynamic-evaluation-count.txt": "x\n",
			},
		},
		{
			name:     "unresolved step output is an evaluation error",
			file:     "condition_evaluation_error.yaml",
			exitCode: 1,
			files: map[string]string{
				"evaluation-error-count.txt": "x\n",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			if tc.file == "condition_root_context.yaml" {
				dagu.Mkdir("condition-work")
				dagu.WriteExecutable("condition-work/loop-shell.sh", `#!/bin/sh
printf 'shell\n' >> shell-invocations.txt
exec sh "$@"
`)
			}
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(tc.exitCode)
			for file, content := range tc.files {
				dagu.ExpectFileContent(file, content)
			}
		})
	}
}

func TestRuntimeLoopEnvironmentAndFeedbackUnix(t *testing.T) {
	t.Parallel()
	skipPOSIXFixtures(t)

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "environment_and_feedback.yaml")
	result.ExpectExitCode(0)

	dagu.ExpectFileContent("feedback-1.txt", "")
	dagu.ExpectFileContains(
		"feedback-2.txt",
		"stdout-marker-1",
		"stderr-marker-1",
		"not met",
		"actual-marker",
		"expected-marker",
		"met",
	)
	feedback, err := os.ReadFile(dagu.ProjectPath("feedback-2.txt"))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(feedback), "not met"))
	require.Contains(t, strings.ReplaceAll(string(feedback), "not met", ""), "met")
	dagu.ExpectFileContains("feedback-3.txt", "stdout-marker-2", "stderr-marker-2")
	dagu.ExpectFileNotContains("feedback-3.txt", "stdout-marker-1", "stderr-marker-1")
	dagu.ExpectFileContent("step-precondition-env.txt", "1\n2\n3\n")

	content, err := os.ReadFile(dagu.ProjectPath("loop-env.txt"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	require.Len(t, lines, 3)
	var feedbackPath string
	for i, line := range lines {
		parts := strings.Split(line, "|")
		require.Len(t, parts, 3)
		require.Equal(t, strconv.Itoa(i+1), parts[0])
		require.Equal(t, "4", parts[1])
		require.NotEmpty(t, parts[2])
		if feedbackPath == "" {
			feedbackPath = parts[2]
		} else {
			require.Equal(t, feedbackPath, parts[2])
		}
	}
	_, err = os.Stat(feedbackPath)
	require.NoError(t, err)
}

func TestRuntimeLoopFeedbackTailLimitUnix(t *testing.T) {
	t.Parallel()
	skipPOSIXFixtures(t)

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "feedback_tail_limit.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContains("large-feedback.txt", "tail-marker", "not met")
	expectTailCappedFeedback(t, dagu, "large-feedback.txt", "head-marker", "tail-marker")
}

func TestRuntimeLoopValueFeedbackTailLimitUnix(t *testing.T) {
	t.Parallel()
	skipPOSIXFixtures(t)

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "value_feedback_tail_limit.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContains("large-value-feedback.txt", "value-tail-marker", "expected-marker", "not met")
	expectTailCappedFeedback(t, dagu, "large-value-feedback.txt", "value-head-marker", "value-tail-marker")
}

func TestRuntimeLoopOutputsAndFilesystemStateUnix(t *testing.T) {
	t.Parallel()
	skipPOSIXFixtures(t)

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "outputs_and_filesystem.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("persistent-filesystem.txt", "x\nx\n")
	dagu.ExpectFileContent("output-consumer.txt", "named-1|captured-1\nnamed-1|captured-2\n")
}

func TestRuntimeLoopHarnessIsolationUnix(t *testing.T) {
	t.Parallel()
	skipPOSIXFixtures(t)

	dagu := harness.NewRunner(t)
	dagu.WriteExecutable("record-agent.sh", `#!/bin/sh
printf 'argc=%s' "$#" >> harness-argv.txt
for arg in "$@"; do
  printf '|%s' "$arg" >> harness-argv.txt
done
printf '\n' >> harness-argv.txt
cat >> harness-stdin.txt
printf '\036' >> harness-stdin.txt
printf 'ok\n'
`)

	result := dagu.Run("start", "harness_isolation.yaml")
	result.ExpectExitCode(0)

	argv, err := os.ReadFile(dagu.ProjectPath("harness-argv.txt"))
	require.NoError(t, err)
	argvLines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	require.Len(t, argvLines, 2)
	require.Equal(t, argvLines[0], argvLines[1])
	require.NotContains(t, strings.ToLower(argvLines[0]), "resume")
	require.NotContains(t, strings.ToLower(argvLines[0]), "session")

	stdin, err := os.ReadFile(dagu.ProjectPath("harness-stdin.txt"))
	require.NoError(t, err)
	stdinInvocations := strings.Split(strings.TrimSuffix(string(stdin), "\x1e"), "\x1e")
	require.Len(t, stdinInvocations, 2)
	require.NotEmpty(t, stdinInvocations[0])
	require.Equal(t, stdinInvocations[0], stdinInvocations[1])
}

func TestRuntimeLoopChatIsolationUnix(t *testing.T) {
	t.Parallel()
	skipPOSIXFixtures(t)

	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var (
		mu       sync.Mutex
		requests [][]message
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		var payload struct {
			Messages []message `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		mu.Lock()
		requests = append(requests, payload.Messages)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
		require.NoError(t, err)
	}))
	defer server.Close()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "--params", "base_url="+server.URL+"/v1", "chat_isolation.yaml")
	result.ExpectExitCode(0)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, requests, 2)
	require.Equal(t, requests[0], requests[1])
	require.Equal(t, []message{{Role: "user", Content: "say ok"}}, requests[0])
}

func TestRuntimeLoopSubDAGCompositionUnix(t *testing.T) {
	t.Parallel()
	skipPOSIXFixtures(t)

	cases := []struct {
		name     string
		file     string
		files    map[string]string
		contains map[string][]string
	}{
		{
			name: "parent and child loops compose with independent indexes",
			file: "subdag_nested_loops.yaml",
			files: map[string]string{
				"parent-loop.txt": "1\n2\n",
				"child-loop.txt":  "1|1\n1|2\n2|1\n2|2\n",
			},
			contains: map[string][]string{
				"nested-child-result.txt": {`"CHILD_VALUE": "child-2"`},
			},
		},
		{
			name: "parent loop environment is not passed to non-looping child",
			file: "subdag_loop_env_scope.yaml",
			files: map[string]string{
				"non-loop-child.txt": "child\nchild\n",
			},
		},
		{
			name: "parallel child runs have independent loop state",
			file: "subdag_parallel_loops.yaml",
			files: map[string]string{
				"parallel-alpha.txt": "1\n2\n",
				"parallel-beta.txt":  "1\n2\n",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			for file, content := range tc.files {
				dagu.ExpectFileContent(file, content)
			}
			for file, parts := range tc.contains {
				dagu.ExpectFileContains(file, parts...)
			}
		})
	}
}

func TestRuntimeLoopResolvedBoundsUnix(t *testing.T) {
	t.Parallel()
	skipPOSIXFixtures(t)

	cases := []struct {
		name     string
		params   string
		exitCode int
	}{
		{name: "valid resolved bounds", params: "iterations=1 interval=0 cap=1", exitCode: 0},
		{name: "max iterations below bound", params: "iterations=0 interval=0 cap=1", exitCode: 1},
		{name: "max iterations above bound", params: "iterations=1001 interval=0 cap=1", exitCode: 1},
		{name: "max iterations is not an integer", params: "iterations=1.5 interval=0 cap=1", exitCode: 1},
		{name: "interval above bound", params: "iterations=1 interval=604801 cap=1", exitCode: 1},
		{name: "interval is not an integer", params: "iterations=1 interval=0.5 cap=1", exitCode: 1},
		{name: "max interval below bound", params: "iterations=1 interval=0 cap=0", exitCode: 1},
		{name: "max interval above bound", params: "iterations=1 interval=0 cap=604801", exitCode: 1},
		{name: "max interval is not an integer", params: "iterations=1 interval=0 cap=1.5", exitCode: 1},
		{name: "unresolved runtime value", params: "iterations=1 interval=0", exitCode: 1},
		{name: "numeric fields have no step output scope", params: "", exitCode: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			if tc.params == "" {
				result := dagu.Run("start", "runtime_bounds_no_step_scope.yaml")
				result.ExpectExitCode(tc.exitCode)
				dagu.ExpectNoFile("bounds-step-ran.txt")
				return
			}
			result := dagu.Run("start", "--params", tc.params, "runtime_resolved_bounds.yaml")
			result.ExpectExitCode(tc.exitCode)
		})
	}
}

func TestRuntimeLoopTimeoutIncludesIntervalUnix(t *testing.T) {
	t.Parallel()
	skipPOSIXFixtures(t)

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "timeout_includes_interval.yaml")
	result.ExpectNonZeroExitCode()
	dagu.ExpectFileContent("timeout-count.txt", "x\n")
	dagu.ExpectNoFile("success-handler.txt")
}

func TestRuntimeLoopAbortDuringIntervalUnix(t *testing.T) {
	t.Parallel()
	skipPOSIXFixtures(t)

	dagu := harness.NewRunner(t)
	running := dagu.Start("start", "abort_during_interval.yaml")
	require.Eventually(t, func() bool {
		content, err := os.ReadFile(dagu.ProjectPath("abort-condition-ran.txt"))
		return err == nil && string(content) == "condition\n"
	}, 5*time.Second, 20*time.Millisecond)
	running.Interrupt()
	result := running.Wait()
	result.ExpectNonZeroExitCode()
	dagu.ExpectFileContent("abort-count.txt", "x\n")
	dagu.ExpectFileContent("abort-handler.txt", "abort\n")
	dagu.ExpectNoFile("success-handler.txt")
}

func TestRuntimeLoopBackoffUnix(t *testing.T) {
	skipPOSIXFixtures(t)

	t.Run("wait follows exponential schedule", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "backoff_schedule.yaml")
		result.ExpectExitCode(0)

		content, err := os.ReadFile(dagu.ProjectPath("backoff-timestamps.txt"))
		require.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(string(content)), "\n")
		require.Len(t, lines, 3)
		timestamps := make([]int64, 0, len(lines))
		for _, line := range lines {
			value, parseErr := strconv.ParseInt(line, 10, 64)
			require.NoError(t, parseErr)
			timestamps = append(timestamps, value)
		}
		require.GreaterOrEqual(t, timestamps[1]-timestamps[0], int64(1))
		require.GreaterOrEqual(t, timestamps[2]-timestamps[1], int64(2))
	})

	t.Run("wait is capped", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "backoff_cap.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("backoff-cap-count.txt", "x\nx\nx\n")
	})

	t.Run("overflow saturates at cap", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "backoff_overflow_saturates.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("backoff-overflow-count.txt", "x\nx\nx\n")
	})
}

func skipPOSIXFixtures(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX shell snippets")
	}
}

func expectTailCappedFeedback(t *testing.T, dagu *harness.Runner, file, headMarker, tailMarker string) {
	t.Helper()

	content, err := os.ReadFile(dagu.ProjectPath(file))
	require.NoError(t, err)
	text := string(content)
	require.NotContains(t, text, headMarker)
	tailIndex := strings.Index(text, tailMarker)
	require.NotEqual(t, -1, tailIndex)
	valueStart := tailIndex
	for valueStart > 0 && text[valueStart-1] == 'x' {
		valueStart--
	}
	require.LessOrEqual(t, tailIndex-valueStart+len(tailMarker), 64*1024)
}
