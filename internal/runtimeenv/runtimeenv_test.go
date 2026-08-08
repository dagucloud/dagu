// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtimeenv_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtimeenv"
	"github.com/stretchr/testify/require"
)

func TestResolveUsesCompletedRuntimeSnapshot(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	dotenvPath := filepath.Join(workDir, ".env")
	require.NoError(t, os.WriteFile(dotenvPath, []byte("VALUE=first\n"), 0o600))

	dag := &ir.DAG{
		WorkingDir: workDir,
		Dotenv:     []string{".env"},
		Env:        []string{"DECLARED=value"},
	}
	resolved, err := runtimeenv.Resolve(context.Background(), dag)
	require.NoError(t, err)
	require.Equal(t, "first", buildenv.ToMap(resolved.Env)["VALUE"])
	require.Equal(t, []string{"DECLARED=value"}, dag.Env)

	dag.Env = resolved.Env
	dag.RuntimeResolved = true
	require.NoError(t, os.WriteFile(dotenvPath, []byte("VALUE=second\n"), 0o600))

	reused, err := runtimeenv.Resolve(context.Background(), dag)
	require.NoError(t, err)
	require.Equal(t, "first", buildenv.ToMap(reused.Env)["VALUE"])
}
