// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagu_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu"
	"github.com/stretchr/testify/require"
)

func engineTestTimeout(timeout time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return timeout * 3
	}
	return timeout
}

func TestEngineRunFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), engineTestTimeout(20*time.Second))
	defer cancel()

	home := t.TempDir()
	dagFile := filepath.Join(home, "embedded-file.yaml")
	checkParamCommand := `test "$FOO" = "bar"`
	checkParamShell := ""
	if runtime.GOOS == "windows" {
		checkParamCommand = `if ($env:FOO -ne "bar") { exit 1 }`
		checkParamShell = `
    shell: powershell`
	}
	writeDAG(t, dagFile, fmt.Sprintf(`
name: embedded-file
steps:
  - name: check-param
    command: %q%s
`, checkParamCommand, checkParamShell))

	engine, err := dagu.New(ctx, dagu.Options{HomeDir: home})
	require.NoError(t, err, "New()")
	defer func() {
		require.NoError(t, engine.Close(context.Background()))
	}()

	run, err := engine.RunFile(ctx, dagFile, dagu.WithParams(map[string]string{"FOO": "bar"}))
	require.NoError(t, err, "RunFile()")
	status, err := run.Wait(ctx)
	require.NoError(t, err, "Wait()")
	require.Equal(t, "succeeded", status.Status)
	require.Equal(t, "embedded-file", status.Name)
	require.NotEmpty(t, status.RunID)
}

func TestEngineRunYAML(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), engineTestTimeout(20*time.Second))
	defer cancel()

	originalWorkingDir, err := os.Getwd()
	require.NoError(t, err, "Getwd()")

	engine, err := dagu.New(ctx, dagu.Options{HomeDir: t.TempDir()})
	require.NoError(t, err, "New()")
	defer func() {
		require.NoError(t, engine.Close(context.Background()))
	}()

	run, err := engine.RunYAML(ctx, []byte(`
name: embedded-yaml
steps:
  - name: hello
    command: echo hello
`))
	require.NoError(t, err, "RunYAML()")
	status, err := run.Wait(ctx)
	require.NoError(t, err, "Wait()")
	require.Equal(t, "succeeded", status.Status)
	require.Equal(t, "embedded-yaml", status.Name)
	currentWorkingDir, err := os.Getwd()
	require.NoError(t, err, "Getwd() after run")
	require.Equal(t, originalWorkingDir, currentWorkingDir)
}

func TestEngineDistributedRequiresCoordinator(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), engineTestTimeout(20*time.Second))
	defer cancel()

	engine, err := dagu.New(ctx, dagu.Options{HomeDir: t.TempDir()})
	require.NoError(t, err, "New()")
	defer func() {
		require.NoError(t, engine.Close(context.Background()))
	}()

	_, err = engine.RunYAML(ctx, []byte(`
name: embedded-distributed
steps:
  - name: hello
    command: echo hello
`), dagu.WithMode(dagu.ExecutionModeDistributed))
	require.Error(t, err)
	require.Contains(t, err.Error(), "coordinator")
}

func writeDAG(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatalf("write DAG: %v", err)
	}
}
