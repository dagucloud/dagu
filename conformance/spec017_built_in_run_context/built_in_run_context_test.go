// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec017_built_in_run_context_test

import (
	"fmt"
	"os"
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
