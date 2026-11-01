// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/require"
)

func TestDryRunCheckMissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ok"), 0o600))

	require.ErrorContains(t, dryRun(t, dir, opRead, map[string]any{"path": "missing-9f3c2b1a.txt"}),
		`field 'with.path': missing-9f3c2b1a.txt: file not found`)
	require.NoError(t, dryRun(t, dir, opRead, map[string]any{"path": "notes.txt"}))
	require.NoError(t, dryRun(t, dir, opRead, map[string]any{"path": "${steps.find.outputs.path}"}),
		"a path still holding a reference cannot be checked")
	require.NoError(t, dryRun(t, dir, opRead, map[string]any{"path": "${OUT}.bak"}),
		"an earlier step's output variable resolves only at run time")
	require.NoError(t, dryRun(t, dir, opStat, map[string]any{"path": "missing-9f3c2b1a.txt", "missing_ok": true}))
	require.ErrorContains(t, dryRun(t, dir, opStat, map[string]any{"path": "missing-9f3c2b1a.txt"}),
		`field 'with.path': missing-9f3c2b1a.txt: file not found`)
	require.ErrorContains(t, dryRun(t, dir, opCopy, map[string]any{"source": "missing-9f3c2b1a.txt", "destination": "out.txt"}),
		`field 'with.source': missing-9f3c2b1a.txt: file not found`)
	require.NoError(t, dryRun(t, dir, opCopy, map[string]any{"source": "missing-9f3c2b1a.txt", "destination": "out.txt", "dry_run": true}),
		"copy with dry_run does not look at the source")
	require.NoError(t, dryRun(t, dir, opWrite, map[string]any{"path": "missing-9f3c2b1a.txt", "content": "x"}),
		"write creates a missing path")

	// A parent that is a file makes the child path absent (ENOTDIR, or
	// ERROR_DIRECTORY on Windows), which the real read would fail on.
	const nested = "notes.txt/missing-9f3c2b1a.txt"
	nestedPath, err := resolveFilePath(dir, nested)
	require.NoError(t, err)
	_, statErr := os.Stat(nestedPath)
	if pathAbsent(statErr) {
		require.ErrorContains(t, dryRun(t, dir, opRead, map[string]any{"path": nested}),
			`field 'with.path': missing-9f3c2b1a.txt: file not found`)
	} else {
		require.NoError(t, dryRun(t, dir, opRead, map[string]any{"path": nested}))
	}

	// Windows chmod only toggles the read-only attribute. It does not deny
	// the stat, and a read-only directory makes the temp dir cleanup fail.
	if goruntime.GOOS == "windows" {
		return
	}
	blocked := filepath.Join(dir, "blocked")
	require.NoError(t, os.Mkdir(blocked, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(blocked, "secret.txt"), []byte("x"), 0o600))
	require.NoError(t, os.Chmod(blocked, 0))
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o700) })
	_, permErr := os.Stat(filepath.Join(blocked, "secret.txt"))
	if permErr != nil && !pathAbsent(permErr) {
		require.NoError(t, dryRun(t, dir, opRead, map[string]any{"path": "blocked/secret.txt"}),
			"a permission error is not a missing file")
	}
}

func dryRun(t *testing.T, workDir, op string, cfg map[string]any) error {
	t.Helper()
	step := ir.Step{
		Name:           "file-step",
		Commands:       []ir.CommandEntry{{Command: op}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: cfg},
	}
	dag := &ir.DAG{Name: "file-dry", WorkingDir: workDir, WorkingDirExplicit: true}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))
	return dryRunCheck(ctx, step)
}
