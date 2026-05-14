// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/require"
)

func TestCustomStepTypes_WorkerWithoutLocalBaseConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("direct exec integration uses /bin/echo")
	}

	baseDir := t.TempDir()
	baseConfigPath := filepath.Join(baseDir, "base.yaml")
	err := os.WriteFile(baseConfigPath, []byte(`
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
`), 0600)
	require.NoError(t, err)

	f := newTestFixture(t, `
name: no-local-custom-step-base
worker_selector:
  test: "true"
steps:
  - name: use-custom-step
    type: greet
    with:
      message: embedded-custom
`, withLogPersistence(), withBaseConfigPath(baseConfigPath), withWorkerBaseConfigPath("/nonexistent/base.yaml"))
	defer f.cleanup()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	status := f.waitForStatus(core.Succeeded, 20*time.Second)
	require.Equal(t, core.Succeeded, status.Status)
	assertLogContains(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "use-custom-step", "embedded-custom")
}

func TestCustomStepTypes_SubDAGBaseConfigPropagation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("direct exec integration uses /bin/echo")
	}

	baseDir := t.TempDir()
	baseConfigPath := filepath.Join(baseDir, "base.yaml")
	err := os.WriteFile(baseConfigPath, []byte(`
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
`), 0600)
	require.NoError(t, err)

	f := newTestFixture(t, `
name: parent-custom-step-base
steps:
  - name: call-child
    action: dag.run
    with:
      dag: child-dag

---
name: child-dag
worker_selector:
  type: test-worker
steps:
  - name: remote-greet
    type: greet
    with:
      message: propagated-custom
`, withLogPersistence(), withBaseConfigPath(baseConfigPath), withWorkerBaseConfigPath("/nonexistent/base.yaml"), withLabels(map[string]string{"type": "test-worker"}))
	defer f.cleanup()

	f.dagWrapper.Agent().RunSuccess(t)
	f.dagWrapper.AssertLatestStatus(t, core.Succeeded)
}

func TestCustomStepTypes_WorkerWithoutLocalBaseConfig_DefaultPrecedence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("integration uses /bin/sh")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh is required for the env precedence integration test")
	}

	baseDir := t.TempDir()
	baseConfigPath := filepath.Join(baseDir, "base.yaml")
	err := os.WriteFile(baseConfigPath, []byte(`
defaults:
  env:
    - LAYERED: default
    - DEFAULT_ONLY: default-only
step_types:
  show_env:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties: {}
    template:
      exec:
        command: /bin/sh
        args:
          - -c
          - printf '%s|%s|%s\n' "$LAYERED" "$DEFAULT_ONLY" "$TEMPLATE_ONLY"
      env:
        - LAYERED: template
        - TEMPLATE_ONLY: template-only
`), 0600)
	require.NoError(t, err)

	f := newTestFixture(t, `
name: no-local-custom-step-default-precedence
worker_selector:
  test: "true"
steps:
  - name: show-layered-env
    type: show_env
    env:
      - LAYERED: call
`, withLogPersistence(), withBaseConfigPath(baseConfigPath), withWorkerBaseConfigPath("/nonexistent/base.yaml"))
	defer f.cleanup()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	status := f.waitForStatus(core.Succeeded, 20*time.Second)
	require.Equal(t, core.Succeeded, status.Status)
	assertLogContains(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "show-layered-env", "call|default-only|template-only")
}
