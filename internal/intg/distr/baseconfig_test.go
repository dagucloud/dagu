// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/require"
)

func TestBaseConfig_EnvVarsExpandOnWorker(t *testing.T) {
	t.Run("baseConfigEnvVarsExpanded", func(t *testing.T) {
		// Create a base config with an env var
		baseDir := t.TempDir()
		baseConfigPath := filepath.Join(baseDir, "base.yaml")
		err := os.WriteFile(baseConfigPath, []byte(`env:
  - GITHUB_URL: "github.com"
`), 0600)
		require.NoError(t, err)

		f := newTestFixture(t, `
name: baseconfig-expand-test
worker_selector:
  test: "true"
steps:
  - name: use-base-env
    run: echo "${GITHUB_URL}"
`, withLogPersistence(), withBaseConfigPath(baseConfigPath))
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, 20*time.Second)
		require.Equal(t, core.Succeeded, status.Status)
		assertLogContains(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "use-base-env", "github.com")
	})
}

func TestBaseConfig_WorkerWithoutLocalBaseConfig(t *testing.T) {
	t.Run("workerUsesEmbeddedBaseConfig", func(t *testing.T) {
		// Create a base config only on the coordinator side
		baseDir := t.TempDir()
		baseConfigPath := filepath.Join(baseDir, "base.yaml")
		err := os.WriteFile(baseConfigPath, []byte(`env:
  - MY_SERVICE_URL: "https://api.example.com"
`), 0600)
		require.NoError(t, err)

		// The worker has a non-existent base config path.
		// This simulates k8s deployments where workers don't have local base configs.
		f := newTestFixture(t, `
name: no-local-base-test
worker_selector:
  test: "true"
steps:
  - name: use-embedded-env
    run: echo "${MY_SERVICE_URL}"
`, withLogPersistence(), withBaseConfigPath(baseConfigPath), withWorkerBaseConfigPath("/nonexistent/base.yaml"))
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, 20*time.Second)
		require.Equal(t, core.Succeeded, status.Status)
		assertLogContains(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "use-embedded-env", "https://api.example.com")
	})
}

func TestBaseConfig_MultipleEnvVarsMerged(t *testing.T) {
	t.Run("baseAndDAGEnvVarsMerged", func(t *testing.T) {
		baseDir := t.TempDir()
		baseConfigPath := filepath.Join(baseDir, "base.yaml")
		err := os.WriteFile(baseConfigPath, []byte(`env:
  - BASE_VAR1: "base-value-1"
  - BASE_VAR2: "base-value-2"
`), 0600)
		require.NoError(t, err)

		f := newTestFixture(t, `
name: merged-env-test
worker_selector:
  test: "true"
env:
  - DAG_VAR: "dag-value"
steps:
  - name: use-all-vars
    run: echo "${BASE_VAR1} ${BASE_VAR2} ${DAG_VAR}"
`, withLogPersistence(), withBaseConfigPath(baseConfigPath))
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, 20*time.Second)
		require.Equal(t, core.Succeeded, status.Status)
		assertLogContains(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "use-all-vars", "base-value-1 base-value-2 dag-value")
	})
}

func TestBaseConfig_SubDAGPropagation(t *testing.T) {
	t.Run("baseConfigForwardedToSubDAG", func(t *testing.T) {
		baseDir := t.TempDir()
		baseConfigPath := filepath.Join(baseDir, "base.yaml")
		err := os.WriteFile(baseConfigPath, []byte(`env:
  - BASE_VAR: "propagated-value"
`), 0600)
		require.NoError(t, err)

		f := newTestFixture(t, `
name: parent-with-base
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
  - name: use-base-var
    run: echo "${BASE_VAR}"
`, withLogPersistence(), withBaseConfigPath(baseConfigPath), withLabels(map[string]string{"type": "test-worker"}))
		defer f.cleanup()

		// Use agent.RunSuccess() (direct execution) instead of the scheduler path
		// to test base config propagation through the sub-DAG dispatch specifically.
		agent := f.dagWrapper.Agent()
		agent.RunSuccess(t)
		f.dagWrapper.AssertLatestStatus(t, core.Succeeded)
	})
}
