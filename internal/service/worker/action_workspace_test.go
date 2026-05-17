// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubWorkspaceBundleClient struct {
	data []byte
	err  error
}

func (c stubWorkspaceBundleClient) PutWorkspaceBundle(context.Context, workspacebundle.Descriptor, []byte) error {
	return nil
}

func (c stubWorkspaceBundleClient) GetWorkspaceBundle(context.Context, string) ([]byte, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.data, nil
}

func TestMaterializeTaskWorkspaceCleansWorkDirOnExtractFailure(t *testing.T) {
	t.Parallel()

	data := []byte("not a gzip bundle")
	workDir := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.MkdirAll(workDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "stale.txt"), []byte("stale"), 0o600))

	task := &coordinatorv1.Task{
		WorkspaceBundleDigest:  workspacebundle.Digest(data),
		WorkspaceBundleSize:    int64(len(data)),
		WorkspaceBundleDagPath: "action.yaml",
	}

	workspace, err := materializeTaskWorkspace(context.Background(), task, stubWorkspaceBundleClient{data: data}, workDir)

	require.Error(t, err)
	assert.Nil(t, workspace)
	assert.Contains(t, err.Error(), "extract workspace bundle")
	_, statErr := os.Stat(workDir)
	require.True(t, errors.Is(statErr, os.ErrNotExist), "workDir should be removed after extract failure")
}
