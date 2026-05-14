// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/test"
)

func posixHomeRelativeTempPath(t *testing.T, pattern string) (string, string) {
	t.Helper()

	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home dir: %v", err)
	}

	tempFile, err := os.CreateTemp(homeDir, pattern)
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if err := tempFile.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	if err := os.Remove(tempFile.Name()); err != nil {
		t.Fatalf("remove temp file: %v", err)
	}

	t.Cleanup(func() {
		_ = os.Remove(tempFile.Name())
	})

	return tempFile.Name(), "~/" + filepath.Base(tempFile.Name())
}

func powerShellEnvOrLiteral(value string) string {
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") && len(value) > 3 {
		return "$env:" + value[2:len(value)-1]
	}
	return fmt.Sprintf("%q", value)
}

func numericPreconditionCommand(left, operator, right string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(
			"if ([int](%s) %s [int](%s)) { exit 0 } else { exit 1 }",
			powerShellEnvOrLiteral(left),
			operator,
			powerShellEnvOrLiteral(right),
		)
	}
	return fmt.Sprintf("test %s %s %s", left, operator, right)
}

func stringPreconditionCommand(left, right string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(
			"if (([string](%s)) -eq ([string](%s))) { exit 0 } else { exit 1 }",
			powerShellEnvOrLiteral(left),
			powerShellEnvOrLiteral(right),
		)
	}
	return fmt.Sprintf("test %s = %s", left, right)
}

func TestPreconditionWithDAGEnvVars(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)
	dag := th.DAG(t, fmt.Sprintf(`
env:
  - DEV_PCENT: "90"
  - DEV_ALERT: "80"
steps:
  - name: check-threshold
    run: echo "alert triggered"
    output: RESULT
    preconditions:
      - condition: %q
`, numericPreconditionCommand("${DEV_PCENT}", "-ge", "${DEV_ALERT}")))
	agent := dag.Agent()
	agent.RunSuccess(t)
	dag.AssertOutputs(t, map[string]any{
		"RESULT": "alert triggered",
	})
}

func TestPreconditionWithDAGEnvVarsNotMet(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)
	dag := th.DAG(t, fmt.Sprintf(`
type: graph
env:
  - DEV_PCENT: "50"
  - DEV_ALERT: "80"
steps:
  - name: check-threshold
    run: echo "alert triggered"
    output: RESULT
    preconditions:
      - condition: %q
`, numericPreconditionCommand("${DEV_PCENT}", "-ge", "${DEV_ALERT}")))
	agent := dag.Agent()
	agent.RunSuccess(t)
	dag.AssertOutputs(t, map[string]any{
		"RESULT": "",
	})
}

func TestDAGLevelPreconditionWithEnvVars(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)
	dag := th.DAG(t, fmt.Sprintf(`
env:
  - ENABLED: "yes"
preconditions:
  - condition: %q
steps:
  - name: run
    run: echo "executed"
    output: RESULT
`, stringPreconditionCommand("${ENABLED}", "yes")))
	agent := dag.Agent()
	agent.RunSuccess(t)
	dag.AssertOutputs(t, map[string]any{
		"RESULT": "executed",
	})
}

func TestPreconditionWithStepOutput(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)
	dag := th.DAG(t, fmt.Sprintf(`
type: graph
steps:
  - name: produce
    run: echo "go"
    output: STEP_RESULT
  - name: consume
    run: echo "ran"
    output: FINAL
    preconditions:
      - condition: %q
    depends: produce
`, stringPreconditionCommand("${STEP_RESULT}", "go")))
	agent := dag.Agent()
	agent.RunSuccess(t)
	dag.AssertOutputs(t, map[string]any{
		"STEP_RESULT": "go",
		"FINAL":       "ran",
	})
}

func TestPreconditionWithHomeRelativeDAGEnvVar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Unix shell test on Windows")
	}

	t.Parallel()
	th := test.Setup(t)
	absolutePath, homeRelativePath := posixHomeRelativeTempPath(t, ".dagu-precondition-*")

	dag := th.DAG(t, fmt.Sprintf(`
type: graph
env:
  - TEST_FILE: %q
steps:
  - name: create
    run: touch $TEST_FILE
  - name: check
    run: echo "ran"
    output: RESULT
    depends: create
    preconditions:
      - condition: test -f $TEST_FILE
`, homeRelativePath))
	agent := dag.Agent()
	agent.RunSuccess(t)

	dag.AssertOutputs(t, map[string]any{
		"RESULT": "ran",
	})

	_, err := os.Stat(absolutePath)
	if err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
}
