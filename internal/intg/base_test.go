// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	runtimepkg "github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/agent"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func readHandlerOutput(raw []byte) string {
	if len(raw) >= 2 {
		if raw[0] == 0xff && raw[1] == 0xfe {
			return fileutil.DecodeString("utf-16", raw)
		}
		if raw[0] == 0xfe && raw[1] == 0xff {
			return fileutil.DecodeString("utf-16be", raw)
		}
	}
	return string(raw)
}

func TestBaseDAGSpecialEnvVarsInHandler(t *testing.T) {
	t.Parallel()

	// Create a temp directory to store base config and output files
	tmpDir := t.TempDir()
	baseConfigPath := filepath.Join(tmpDir, "base.yaml")
	outputFile := filepath.Join(tmpDir, "handler_output.txt")
	outputFileForShell := filepath.ToSlash(outputFile)

	// Create base DAG with handler_on: failure that captures special env vars
	baseConfig := `handler_on:
  failure:
    run: |
      echo "DAG_NAME=${DAG_NAME}" >> "` + outputFileForShell + `"
      echo "DAG_RUN_ID=${DAG_RUN_ID}" >> "` + outputFileForShell + `"
      echo "DAG_RUN_LOG_FILE=${DAG_RUN_LOG_FILE}" >> "` + outputFileForShell + `"
`
	require.NoError(t, os.WriteFile(baseConfigPath, []byte(baseConfig), 0600))

	// Setup test helper
	th := test.Setup(t)

	// Create a DAG file that will fail
	dagContent := `steps:
  - name: failing-step
    run: exit 1
`
	dagFile := th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "test-base-env", []byte(dagContent))

	// Load the DAG with base config
	dag, err := spec.Load(th.Context, dagFile, spec.WithBaseConfig(baseConfigPath))
	require.NoError(t, err)

	// Verify base config was applied - handler_on should be set
	require.NotNil(t, dag.HandlerOn.Failure, "failure handler from base config should be set")

	// Create agent and run
	dagRunID := uuid.New().String()
	logDir := th.Config.Paths.LogDir
	logFile := filepath.Join(logDir, dagRunID+".log")
	root := exec.NewDAGRunRef(dag.Name, dagRunID)

	drm := runtimepkg.NewManager(th.DAGRunStore, th.ProcStore, th.Config)

	a := agent.New(
		dagRunID,
		dag,
		logDir,
		logFile,
		drm,
		th.DAGStore,
		agent.Options{
			DAGRunStore:     th.DAGRunStore,
			ServiceRegistry: th.ServiceRegistry,
			RootDAGRun:      root,
			PeerConfig:      th.Config.Core.Peer,
		},
	)

	// Run the agent - expect failure
	err = a.Run(th.Context)
	require.Error(t, err)

	// Verify the DAG failed
	status := a.Status(th.Context)
	require.Equal(t, core.Failed, status.Status)

	// Read the output file and verify special env vars were available
	output, err := os.ReadFile(outputFile)
	require.NoError(t, err, "handler output file should exist")

	outputStr := readHandlerOutput(output)
	require.Contains(t, outputStr, "DAG_NAME=", "DAG_NAME should be set")
	require.Contains(t, outputStr, "DAG_RUN_ID=", "DAG_RUN_ID should be set")
	require.Contains(t, outputStr, "DAG_RUN_LOG_FILE=", "DAG_RUN_LOG_FILE should be set")

	// Verify the values are not empty
	require.NotContains(t, outputStr, "DAG_NAME=\n", "DAG_NAME should not be empty")
	require.NotContains(t, outputStr, "DAG_RUN_ID=\n", "DAG_RUN_ID should not be empty")
	require.NotContains(t, outputStr, "DAG_RUN_LOG_FILE=\n", "DAG_RUN_LOG_FILE should not be empty")
}

func TestSkipBaseHandlers_SubDAGDoesNotInheritHandlers(t *testing.T) {
	t.Parallel()

	// Create a temp directory to store base config
	tmpDir := t.TempDir()
	baseConfigPath := filepath.Join(tmpDir, "base.yaml")
	markerFile := filepath.Join(tmpDir, "marker.txt")
	markerFileForShell := filepath.ToSlash(markerFile)

	// Create base DAG with handler_on: failure that writes a marker file
	baseConfig := `handler_on:
  failure:
    run: echo "BASE_FAILURE_HANDLER_RAN" >> "` + markerFileForShell + `"
`
	require.NoError(t, os.WriteFile(baseConfigPath, []byte(baseConfig), 0600))

	// Load a DAG WITHOUT skip base handlers - should have handler
	th := test.Setup(t)
	dagContent := `steps:
  - name: failing-step
    run: exit 1
`
	dagFile := th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "test-no-skip", []byte(dagContent))

	// Load without skip - should have failure handler from base config
	dagWithHandler, err := spec.Load(th.Context, dagFile, spec.WithBaseConfig(baseConfigPath))
	require.NoError(t, err)
	require.NotNil(t, dagWithHandler.HandlerOn.Failure, "failure handler from base config should be set")

	// Load WITH skip base handlers - should NOT have handler
	dagWithoutHandler, err := spec.Load(th.Context, dagFile, spec.WithBaseConfig(baseConfigPath), spec.WithSkipBaseHandlers())
	require.NoError(t, err)
	require.Nil(t, dagWithoutHandler.HandlerOn.Failure, "failure handler should NOT be inherited when skip flag is set")
}

func TestSkipBaseHandlers_ExplicitHandlersStillWork(t *testing.T) {
	t.Parallel()

	// Create a temp directory to store base config
	tmpDir := t.TempDir()
	baseConfigPath := filepath.Join(tmpDir, "base.yaml")
	baseMarkerFile := filepath.Join(tmpDir, "base_marker.txt")
	dagMarkerFile := filepath.Join(tmpDir, "dag_marker.txt")
	baseMarkerFileForShell := filepath.ToSlash(baseMarkerFile)
	dagMarkerFileForShell := filepath.ToSlash(dagMarkerFile)

	// Create base DAG with handler_on: failure
	baseConfig := `handler_on:
  failure:
    run: echo "BASE" >> "` + baseMarkerFileForShell + `"
`
	require.NoError(t, os.WriteFile(baseConfigPath, []byte(baseConfig), 0600))

	// Setup test helper
	th := test.Setup(t)

	// Create a DAG file with its own failure handler
	dagContent := `handler_on:
  failure:
    run: echo "DAG" >> "` + dagMarkerFileForShell + `"

steps:
  - name: failing-step
    run: exit 1
`
	dagFile := th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "test-explicit-handler", []byte(dagContent))

	// Load WITH skip base handlers - should have DAG's own handler
	dag, err := spec.Load(th.Context, dagFile, spec.WithBaseConfig(baseConfigPath), spec.WithSkipBaseHandlers())
	require.NoError(t, err)
	require.NotNil(t, dag.HandlerOn.Failure, "DAG's own failure handler should be present")

	// Run the DAG
	dagRunID := uuid.New().String()
	logDir := th.Config.Paths.LogDir
	logFile := filepath.Join(logDir, dagRunID+".log")
	root := exec.NewDAGRunRef(dag.Name, dagRunID)

	drm := runtimepkg.NewManager(th.DAGRunStore, th.ProcStore, th.Config)

	a := agent.New(
		dagRunID,
		dag,
		logDir,
		logFile,
		drm,
		th.DAGStore,
		agent.Options{
			DAGRunStore:     th.DAGRunStore,
			ServiceRegistry: th.ServiceRegistry,
			RootDAGRun:      root,
			PeerConfig:      th.Config.Core.Peer,
		},
	)

	// Run the agent - expect failure
	err = a.Run(th.Context)
	require.Error(t, err)

	// Wait for the handler file to be written
	require.Eventually(t, func() bool {
		_, err := os.Stat(dagMarkerFile)
		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "DAG's failure handler should have written marker file")

	// Verify DAG's own handler ran
	dagOutput, err := os.ReadFile(dagMarkerFile)
	require.NoError(t, err)
	require.Contains(t, readHandlerOutput(dagOutput), "DAG", "DAG's own failure handler should have run")

	// Verify base handler did NOT run
	_, err = os.ReadFile(baseMarkerFile)
	require.True(t, os.IsNotExist(err), "base failure handler should NOT have run")
}

func TestSkipBaseHandlers_AllHandlerTypesSkipped(t *testing.T) {
	t.Parallel()

	// Create a temp directory to store base config
	tmpDir := t.TempDir()
	baseConfigPath := filepath.Join(tmpDir, "base.yaml")

	// Create base DAG with all handler types
	baseConfig := `handler_on:
  init:
    run: "true"
  success:
    run: "true"
  failure:
    run: "true"
  abort:
    run: "true"
  exit:
    run: "true"
`
	require.NoError(t, os.WriteFile(baseConfigPath, []byte(baseConfig), 0600))

	// Setup test helper
	th := test.Setup(t)

	// Create a DAG file
	dagContent := `steps:
  - name: step1
    run: "true"
`
	dagFile := th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "test-all-handlers", []byte(dagContent))

	// Load without skip - all handlers should be set
	dagWithHandlers, err := spec.Load(th.Context, dagFile, spec.WithBaseConfig(baseConfigPath))
	require.NoError(t, err)
	require.NotNil(t, dagWithHandlers.HandlerOn.Init, "init handler should be set")
	require.NotNil(t, dagWithHandlers.HandlerOn.Success, "success handler should be set")
	require.NotNil(t, dagWithHandlers.HandlerOn.Failure, "failure handler should be set")
	require.NotNil(t, dagWithHandlers.HandlerOn.Abort, "abort handler should be set")
	require.NotNil(t, dagWithHandlers.HandlerOn.Exit, "exit handler should be set")

	// Load WITH skip - no handlers should be set
	dagWithoutHandlers, err := spec.Load(th.Context, dagFile, spec.WithBaseConfig(baseConfigPath), spec.WithSkipBaseHandlers())
	require.NoError(t, err)
	require.Nil(t, dagWithoutHandlers.HandlerOn.Init, "init handler should NOT be set")
	require.Nil(t, dagWithoutHandlers.HandlerOn.Success, "success handler should NOT be set")
	require.Nil(t, dagWithoutHandlers.HandlerOn.Failure, "failure handler should NOT be set")
	require.Nil(t, dagWithoutHandlers.HandlerOn.Abort, "abort handler should NOT be set")
	require.Nil(t, dagWithoutHandlers.HandlerOn.Exit, "exit handler should NOT be set")
}
