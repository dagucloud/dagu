// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestSecretMasking(t *testing.T) {
	t.Parallel()

	t.Run("FileProviderMasksInStdout", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		// Create a temporary secret file
		secretValue := "super-secret-token-12345"
		secretFile := th.TempFile(t, "secret.txt", []byte(secretValue))

		dag := th.DAG(t, `
secrets:
  - name: API_TOKEN
    provider: file
    key: `+secretFile+`

steps:
  - name: echo-secret
    run: echo "Token is ${API_TOKEN}"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)

		// Get the dag-run status to find the stdout file
		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		require.Len(t, status.Nodes, 1)

		// Read the stdout file - should contain masked value
		node := status.Nodes[0]
		stdoutFile := node.Stdout
		require.NotEmpty(t, stdoutFile, "stdout file should be set")

		stdoutContent, err := os.ReadFile(stdoutFile)
		require.NoError(t, err)

		// The secret value should be masked
		require.NotContains(t, string(stdoutContent), secretValue, "secret should be masked in stdout")
		require.Contains(t, string(stdoutContent), "*******", "masked placeholder should appear")
	})

	t.Run("EnvProviderMasksInOutput", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		secretValue := "my-api-key-67890"

		dag := th.DAG(t, `
env:
  - MY_SECRET=`+secretValue+`

secrets:
  - name: SECRET_FROM_ENV
    provider: env
    key: MY_SECRET

steps:
  - name: use-secret
    run: echo "Secret value is ${SECRET_FROM_ENV}"
    output: OUTPUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)

		// Get stdout file and verify masking
		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		require.Len(t, status.Nodes, 1)

		node := status.Nodes[0]
		stdoutFile := node.Stdout
		stdoutContent, err := os.ReadFile(stdoutFile)
		require.NoError(t, err)

		require.NotContains(t, string(stdoutContent), secretValue, "secret should be masked")
		require.Contains(t, string(stdoutContent), "*******", "masked value should appear")
	})

	t.Run("EnvSecretFromDotenvFile", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		secretValue := "dotenv-loaded-secret-2468"
		dotenvContent := fmt.Sprintf("DOTENV_SECRET=%s\n", secretValue)
		dotenvPath := th.TempFile(t, ".env", []byte(dotenvContent))
		workDir := filepath.Dir(dotenvPath)

		dag := th.DAG(t, fmt.Sprintf(`
working_dir: %q
dotenv: .env

secrets:
  - name: DOTENV_SECRET
    provider: env
    key: DOTENV_SECRET

steps:
  - name: use-dotenv-secret
    run: echo "Dotenv secret is ${DOTENV_SECRET}"
    output: RESULT
`, workDir))

		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)

		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		require.Len(t, status.Nodes, 1)

		stdoutContent, err := os.ReadFile(status.Nodes[0].Stdout)
		require.NoError(t, err)

		stdout := string(stdoutContent)

		require.NotContains(t, stdout, secretValue, "secret from dotenv should be masked in stdout")
		require.Contains(t, stdout, "*******", "masked value should appear")
		require.Contains(t, stdout, "Dotenv secret is", "expected log message")
	})

	t.Run("MultipleSecretsMaskedCorrectly", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		secret1 := "secret-one-abc"
		secret2 := "secret-two-xyz"
		secretFile1 := th.TempFile(t, "secret1.txt", []byte(secret1))
		secretFile2 := th.TempFile(t, "secret2.txt", []byte(secret2))

		dag := th.DAG(t, `
secrets:
  - name: FIRST_SECRET
    provider: file
    key: `+secretFile1+`
  - name: SECOND_SECRET
    provider: file
    key: `+secretFile2+`

steps:
  - name: echo-both
    run: |
      `+test.LabeledExpandedOutput("First: ", "${FIRST_SECRET}")+`
      `+test.LabeledExpandedOutput("Second: ", "${SECOND_SECRET}")+`
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)

		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		require.Len(t, status.Nodes, 1)

		node := status.Nodes[0]
		stdoutContent, err := os.ReadFile(node.Stdout)
		require.NoError(t, err)

		// Both secrets should be masked
		require.NotContains(t, string(stdoutContent), secret1)
		require.NotContains(t, string(stdoutContent), secret2)
		require.Contains(t, string(stdoutContent), "*******")
	})

	t.Run("SecretMaskedInLogFile", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		secretValue := "log-secret-999"
		secretFile := th.TempFile(t, "log-secret.txt", []byte(secretValue))

		dag := th.DAG(t, `
secrets:
  - name: LOG_SECRET
    provider: file
    key: `+secretFile+`

steps:
  - name: log-step
    run: |
      echo "Before secret"
      echo "The secret is ${LOG_SECRET}"
      echo "After secret"
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)

		// Get node and read both stdout and the main log
		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		require.Len(t, status.Nodes, 1)

		node := status.Nodes[0]
		stdoutContent, err := os.ReadFile(node.Stdout)
		require.NoError(t, err)

		// Verify secret is masked in stdout
		require.NotContains(t, string(stdoutContent), secretValue)
		require.Contains(t, string(stdoutContent), "Before secret")
		require.Contains(t, string(stdoutContent), "After secret")
	})

	t.Run("NoSecretsNoMasking", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		normalValue := "not-a-secret"

		dag := th.DAG(t, `
steps:
  - name: normal-output
    run: echo "Value is `+normalValue+`"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)

		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		require.Len(t, status.Nodes, 1)

		node := status.Nodes[0]
		stdoutContent, err := os.ReadFile(node.Stdout)
		require.NoError(t, err)

		// Normal value should not be masked
		require.Contains(t, string(stdoutContent), normalValue)
		require.NotContains(t, string(stdoutContent), "*******")
	})

	t.Run("RelativePathWithWorkingDir", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		// Keep the working dir under the helper temp root so Windows cleanup
		// does not fail while the runtime is still releasing file handles.
		workDir := filepath.Join(th.Config.Paths.DataDir, "relative-secret-working-dir")
		require.NoError(t, os.MkdirAll(workDir, 0750))
		secretValue := "relative-path-secret-999"

		// Create secret file directly in the working directory
		secretFilePath := workDir + "/secret.txt"
		require.NoError(t, os.WriteFile(secretFilePath, []byte(secretValue), 0600))

		dag := th.DAG(t, `
working_dir: `+workDir+`

secrets:
  - name: RELATIVE_SECRET
    provider: file
    key: secret.txt

steps:
  - name: use-relative-secret
    run: |
      echo "Secret from relative path ${RELATIVE_SECRET}"
    output: OUTPUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)

		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		require.Len(t, status.Nodes, 1)

		node := status.Nodes[0]
		stdoutContent, err := os.ReadFile(node.Stdout)
		require.NoError(t, err)

		// Secret should be masked even when loaded from relative path
		require.NotContains(t, string(stdoutContent), secretValue, "secret loaded from relative path should be masked")
		require.Contains(t, string(stdoutContent), "*******", "masked value should appear")
		require.Contains(t, string(stdoutContent), "Secret from relative path", "log message should be present")
	})

	t.Run("EmptySecretValueDoesNotMaskEverything", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		// Test that empty secret values don't cause masking of all output
		// (which would happen if strings.ReplaceAll is called with empty string)
		normalOutput := "This is normal output that should not be masked"

		dag := th.DAG(t, `
env:
  - EMPTY_SECRET=

secrets:
  - name: EMPTY_SECRET_ALIAS
    provider: env
    key: EMPTY_SECRET

steps:
  - name: test-empty-secret
    run: echo "`+normalOutput+`"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)

		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		require.Len(t, status.Nodes, 1)

		node := status.Nodes[0]
		stdoutContent, err := os.ReadFile(node.Stdout)
		require.NoError(t, err)

		// The output should NOT be masked (no stream of *******)
		require.Contains(t, string(stdoutContent), normalOutput, "output should not be masked when secret is empty")
		// Should not have replaced every character with *******
		require.NotContains(t, string(stdoutContent), "*******T*******h*******i*******s", "should not mask between every character")
	})

	t.Run("RelativePathFromDAGLocation", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		// Keep the working dir under the helper temp root so Windows cleanup
		// does not fail while the runtime is still releasing file handles.
		workDir := filepath.Join(th.Config.Paths.DataDir, "dag-location-secret-working-dir")
		require.NoError(t, os.MkdirAll(workDir, 0750))

		secretValue := "dag-location-secret-123"

		// Ensure DAGsDir exists
		require.NoError(t, os.MkdirAll(th.Config.Paths.DAGsDir, 0750))

		// Create secret file in the DAG directory (th.Config.Paths.DAGsDir)
		// The DAG method creates DAG files in this directory, so we put the secret there too
		secretFilePath := th.Config.Paths.DAGsDir + "/secret.txt"
		require.NoError(t, os.WriteFile(secretFilePath, []byte(secretValue), 0600))

		// Create DAG with workingDir set to a different directory
		// The secret file is NOT in workingDir, but in the DAG file's directory
		dag := th.DAG(t, `
working_dir: `+workDir+`

secrets:
  - name: DAG_SECRET
    provider: file
    key: secret.txt

steps:
  - name: use-dag-location-secret
    run: |
      echo "Secret from DAG location ${DAG_SECRET}"
    output: OUTPUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)

		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		require.Len(t, status.Nodes, 1)

		node := status.Nodes[0]
		stdoutContent, err := os.ReadFile(node.Stdout)
		require.NoError(t, err)

		// Secret should be found (from DAG location) and masked
		require.NotContains(t, string(stdoutContent), secretValue, "secret loaded from DAG location should be masked")
		require.Contains(t, string(stdoutContent), "*******", "masked value should appear")
		require.Contains(t, string(stdoutContent), "Secret from DAG location", "log message should be present")
	})

	t.Run("SecretsInStepLevelEnv", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		// Create secret file
		secretValue := "super-secret-db-password-789"
		secretFile := th.TempFile(t, "db-password.txt", []byte(secretValue))

		// Set up environment variable for env provider
		apiTokenValue := "api-token-xyz-123"

		dag := th.DAG(t, `
env:
  - PROD_API_TOKEN: `+apiTokenValue+`

secrets:
  - name: API_TOKEN
    provider: env
    key: PROD_API_TOKEN
  - name: DB_PASSWORD
    provider: file
    key: `+secretFile+`

steps:
  - name: use-secrets-in-env
    run: |
      `+test.LabeledExpandedOutput("Auth header: ", "${AUTH_HEADER}")+`
      `+test.LabeledExpandedOutput("DB pass: ", "${DB_PASS}")+`
      `+test.LabeledExpandedOutput("Strict mode: ", "${STRICT_MODE}")+`
    env:
      AUTH_HEADER: "Bearer ${API_TOKEN}"
      DB_PASS: "${DB_PASSWORD}"
      STRICT_MODE: "1"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)

		// Get stdout file and verify masking
		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		require.Len(t, status.Nodes, 1)

		node := status.Nodes[0]
		stdoutContent, err := os.ReadFile(node.Stdout)
		require.NoError(t, err)

		stdout := string(stdoutContent)

		// Both secrets should be masked in output
		require.NotContains(t, stdout, apiTokenValue, "API token should be masked")
		require.NotContains(t, stdout, secretValue, "DB password should be masked")
		require.Contains(t, stdout, "*******", "masked value should appear")

		// Non-secret values should appear
		require.Contains(t, stdout, "Strict mode: 1", "non-secret env var should appear")
		require.Contains(t, stdout, "Auth header: Bearer", "prefix should appear")
		require.Contains(t, stdout, "DB pass:", "label should appear")
	})
}
