// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec017_built_in_run_context_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestValidateBuiltInRunContextNotices(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("validate", "validation_notices.yaml")
	result.ExpectExitCode(0)
	result.ExpectStdout("")
	// References to fields the spec does not define are reported by default.
	result.ExpectStderrContains(
		"${context.paths.context}",
		"${context.trigger.payload}",
		"${context.run.foo}",
		"${context.run.id.extra}",
		"reason=unknown_context_field",
		"was left unchanged",
	)
	// Supported fields carry a value only during a run, so validation stays
	// quiet about them unless the operator asks.
	result.ExpectStderrNotContains(
		"${unrelated.context}",
		"reason=namespace_unavailable",
	)

	verbose := dagu.Run("validate", "--show-unresolved", "validation_notices.yaml")
	verbose.ExpectExitCode(0)
	verbose.ExpectStderrContains(
		"${context.run.id}",
		"${context.attempt.id}",
		"${context.run.status}",
		"${context.trigger.actor}",
		"${context.profile.name}",
		"${context.pushback.iteration}",
		"reason=namespace_unavailable",
	)
	dagu.ExpectNoFile("validation-notices.txt")
}

func TestRuntimeBuiltInRunContext(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run(
		"start",
		"--run-id=spec017-run",
		"--trigger-type=scheduler",
		"--schedule-time=2026-03-13T10:00:00+09:00",
		"runtime_context.yaml",
	)
	result.ExpectExitCode(0)
	dagu.ExpectFileContains(
		"run-context.txt",
		"dag=builtin_runtime_context",
		"run=spec017-run",
		"root_name=${context.run.root_name}",
		"root_id=${context.run.root_id}",
		"root_env=spec017-run",
		"trigger=scheduler",
		"scheduled=2026-03-13T01:00:00Z",
		"attempt=",
		"step_id=capture",
		"step_name=capture",
		"env_run=spec017-run",
		"env_step=capture",
		"paths_ready=yes",
	)
	dagu.ExpectFileNotContains(
		"run-context.txt",
		"${context.attempt.id}",
		"${context.attempt.started_at}",
		"${context.paths.step_stdout_file}",
		"${context.paths.step_stderr_file}",
		"${context.paths.step_output_file}",
	)
}

func TestRuntimeScheduleWindow(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run(
		"start",
		"--run-id=spec017-window",
		"--trigger-type=scheduler",
		"--schedule-time=2026-03-13T12:00:00+09:00",
		"schedule_window.yaml",
	)
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("schedule-window.txt", strings.Join([]string{
		"scheduled=2026-03-13T03:00:00Z",
		"start=2026-03-13T02:30:00Z",
		"end=2026-03-13T03:00:00Z",
		"next=2026-03-13T03:30:00Z",
		"timezone=Asia/Tokyo",
		"",
	}, "\n"))
}

// A manual run started for a slot gets the window the scheduler's run of
// that slot gets. A time without an offset is read in the schedule's zone.
func TestScheduledAtStartsRunForSlot(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run(
		"start",
		"--run-id=spec017-scheduled-at",
		"--scheduled-at=2026-03-13T12:00",
		"schedule_window.yaml",
	)
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("schedule-window.txt", strings.Join([]string{
		"scheduled=2026-03-13T03:00:00Z",
		"start=2026-03-13T02:30:00Z",
		"end=2026-03-13T03:00:00Z",
		"next=2026-03-13T03:30:00Z",
		"timezone=Asia/Tokyo",
		"",
	}, "\n"))
}

func TestScheduledAtRejectsFutureSlot(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run(
		"start",
		"--run-id=spec017-scheduled-at-future",
		"--scheduled-at=2999-01-01T00:00",
		"schedule_window.yaml",
	)
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("scheduled time 2999-01-01T00:00:00+09:00 is later than now")
	dagu.ExpectNoFile("schedule-window.txt")
}

// A dry run starts nothing, so it previews a slot that has not come.
func TestDryScheduledAtPreviewsFutureSlot(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run(
		"dry",
		"--scheduled-at=2999-01-01T00:00",
		"schedule_window.yaml",
	)
	result.ExpectExitCode(0)
	stdout := result.Stdout()
	for _, want := range []string{
		"Scheduled at: 2999-01-01T00:00:00+09:00 (Asia/Tokyo)",
		"Window:       2998-12-31T23:30:00+09:00 to 2999-01-01T00:00:00+09:00",
		"Next fire:    2999-01-01T00:30:00+09:00",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
	dagu.ExpectNoFile("schedule-window.txt")
}

// The window is recorded when the run is created, so a retry keeps it after
// the schedule is edited.
func TestRetryKeepsScheduleWindow(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	// Both commands must read the same run history.
	env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}
	dagu.RunWithEnv(
		env,
		"start",
		"--run-id=spec017-window-retry",
		"--trigger-type=scheduler",
		"--schedule-time=2026-03-13T12:00:00+09:00",
		"schedule_window_retry.yaml",
	).ExpectNonZeroExitCode()
	want := "start=2026-03-13T02:30:00Z\nnext=2026-03-13T03:30:00Z\n"
	dagu.ExpectFileContent("schedule-window.txt", want)

	edited := strings.Replace(readProjectFile(t, dagu, "schedule_window_retry.yaml"),
		"*/30 * * * *", "0 * * * *", 1)
	dagu.WriteFile("schedule_window_retry.yaml", edited)
	dagu.WriteFile("retry-ok", "")
	dagu.RunWithEnv(env, "retry", "--run-id=spec017-window-retry", "builtin_schedule_window_retry").ExpectExitCode(0)

	dagu.ExpectFileContent("schedule-window.txt", want)
}

func readProjectFile(t *testing.T, dagu *harness.Runner, name string) string {
	t.Helper()
	data, err := os.ReadFile(dagu.ProjectPath(name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// A DAG with no recurring start schedule has no previous or next fire, so
// only the end of its window and the server zone are known.
func TestRuntimeScheduleWindowWithoutSchedule(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(
		[]string{"DAGU_TZ=Europe/Paris"},
		"start",
		"--run-id=spec017-window-unscheduled",
		"--trigger-type=scheduler",
		"--schedule-time=2026-03-13T12:00:00+01:00",
		"schedule_window_unscheduled.yaml",
	)
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("schedule-window.txt", strings.Join([]string{
		"start=${context.run.interval_start}",
		"end=2026-03-13T11:00:00Z",
		"next=${context.run.next_scheduled_at}",
		"timezone=Europe/Paris",
		"",
	}, "\n"))
}

func TestLegacyBuiltInRunContextAliases(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "--run-id=spec017-legacy", "legacy_aliases.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContains(
		"legacy-context-aliases.txt",
		"dag=builtin_legacy_context_aliases",
		"run=spec017-legacy",
		"started=",
		"attempt=",
		"step=capture",
	)
	dagu.ExpectFileNotContains(
		"legacy-context-aliases.txt",
		"${dag.name}",
		"${run.id}",
		"${run.started_at}",
		"${attempt.id}",
		"${step.name}",
	)
}

func TestHandlerBuiltInRunContext(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "--run-id=spec017-handler", "handler_context.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContains(
		"handler-context.txt",
		"status=succeeded",
		"env_status=succeeded",
		"run=spec017-handler",
		"stdout=${context.paths.step_stdout_file}",
		"stderr=${context.paths.step_stderr_file}",
		"env_stdout=UNSET",
		"env_stderr=UNSET",
	)
	dagu.ExpectFileNotContains(
		"handler-context.txt",
		"${context.run.status}",
	)
}

// A repeating step sees the index of each execution in the reference, in step
// env, and in the environment variable. A step without repeat_policy has none.
func TestRepeatIndexBuiltInRunContext(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "repeat_index.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent(
		"repeat-index.txt",
		"0|0|0\n1|1|1\n2|2|2\nonce=${context.step.repeat_index}|UNSET\n",
	)
}

func TestUnknownContextFieldsStaySilentAtRuntime(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "unsupported_context_text.yaml")
	result.ExpectExitCode(0)
	result.ExpectStderrNotContains("was left unchanged")
	dagu.ExpectFileContent(
		"unsupported-context.txt",
		"${context.paths.context}\n${context.trigger.payload}\n${context.run.foo}\n${context.run.id.extra}\n${unrelated.context}\n",
	)
}

// traceContextDAG enables tracing with an exporter endpoint that accepts no
// connection. The run must still succeed: export failures only log.
const traceContextDAG = `name: builtin_trace_context
working_dir: .
otel:
  enabled: true
  endpoint: "127.0.0.1:%d"
  insecure: true
  timeout: 1s
env:
  - TRACEPARENT=00-11111111111111111111111111111111-2222222222222222-01
steps:
  - id: first
    run: |
      printf 'first=%%s|%%s\n' "$TRACEPARENT" "${TRACESTATE-UNSET}" >> trace-context.txt
  - id: second
    depends: [first]
    run: |
      printf 'second=%%s|%%s\n' "$TRACEPARENT" "${TRACESTATE-UNSET}" >> trace-context.txt
  - id: own
    depends: [second]
    env:
      - TRACEPARENT=00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
    run: |
      printf 'own=%%s|%%s\n' "$TRACEPARENT" "${TRACESTATE-UNSET}" >> trace-context.txt
  - id: cleared
    depends: [own]
    env:
      - TRACEPARENT=
    run: |
      printf 'cleared=%%s|%%s\n' "$TRACEPARENT" "${TRACESTATE-UNSET}" >> trace-context.txt
`

var traceParentPattern = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-01$`)

func TestStepTraceContext(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.WriteFile("trace_context.yaml", fmt.Sprintf(traceContextDAG, harness.FreePort(t)))
	result := dagu.Run("start", "trace_context.yaml")
	result.ExpectExitCode(0)

	content, err := os.ReadFile(dagu.ProjectPath("trace-context.txt"))
	if err != nil {
		t.Fatalf("reading trace-context.txt: %v", err)
	}
	values := map[string]string{}
	for line := range strings.Lines(string(content)) {
		key, value, _ := strings.Cut(strings.TrimRight(line, "\r\n"), "=")
		values[key] = value
	}

	first := traceParentPattern.FindStringSubmatch(strings.TrimSuffix(values["first"], "|UNSET"))
	second := traceParentPattern.FindStringSubmatch(strings.TrimSuffix(values["second"], "|UNSET"))
	if first == nil || second == nil {
		t.Fatalf("step span trace context missing: %q", content)
	}
	// Both steps belong to the trace of the DAG run, not to the trace of
	// the root-scope TRACEPARENT, and each step has its own span.
	if first[1] != second[1] {
		t.Errorf("steps are in different traces: %s and %s", first[1], second[1])
	}
	if first[1] == "11111111111111111111111111111111" {
		t.Errorf("root-scope TRACEPARENT was not replaced: %q", values["first"])
	}
	if first[2] == second[2] {
		t.Errorf("steps share the parent span ID %s", first[2])
	}
	if want := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01|UNSET"; values["own"] != want {
		t.Errorf("step env TRACEPARENT: got %q, want %q", values["own"], want)
	}
	if want := "|UNSET"; values["cleared"] != want {
		t.Errorf("empty step env TRACEPARENT: got %q, want %q", values["cleared"], want)
	}
}

func TestStepTraceContextDisabled(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "trace_context_disabled.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("trace-context.txt", "traceparent=UNSET\ntracestate=UNSET\n")
}
