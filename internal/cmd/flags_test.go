// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInternalHierarchyFlagsHiddenFromUsage(t *testing.T) {
	t.Parallel()

	startCmd := cmd.Start()
	retryCmd := cmd.Retry()

	require.NotNil(t, startCmd.Flags().Lookup("root"))
	require.NotNil(t, startCmd.Flags().Lookup("parent"))
	require.NotNil(t, retryCmd.Flags().Lookup("root"))

	assert.True(t, startCmd.Flags().Lookup("root").Hidden)
	assert.True(t, startCmd.Flags().Lookup("parent").Hidden)
	assert.True(t, retryCmd.Flags().Lookup("root").Hidden)

	assert.NotContains(t, startCmd.UsageString(), "--root")
	assert.NotContains(t, startCmd.UsageString(), "--parent")
	assert.NotContains(t, retryCmd.UsageString(), "--root")
}

func TestHiddenHierarchyFlagsStillParse(t *testing.T) {
	t.Parallel()

	startCmd := cmd.Start()
	require.NoError(t, startCmd.Flags().Parse([]string{
		"--root=root-dag:root-run",
		"--parent=parent-dag:parent-run",
	}))
	rootStart, err := startCmd.Flags().GetString("root")
	require.NoError(t, err)
	parentStart, err := startCmd.Flags().GetString("parent")
	require.NoError(t, err)
	assert.Equal(t, "root-dag:root-run", rootStart)
	assert.Equal(t, "parent-dag:parent-run", parentStart)

	retryCmd := cmd.Retry()
	require.NoError(t, retryCmd.Flags().Parse([]string{
		"--root=root-dag:root-run",
	}))
	rootRetry, err := retryCmd.Flags().GetString("root")
	require.NoError(t, err)
	assert.Equal(t, "root-dag:root-run", rootRetry)
}

func TestRetryCommandAcceptsDefaultWorkingDirFlag(t *testing.T) {
	t.Parallel()

	retryCmd := cmd.Retry()
	require.NotNil(t, retryCmd.Flags().Lookup("default-working-dir"))
	require.NoError(t, retryCmd.Flags().Parse([]string{
		"--default-working-dir=/tmp/dags",
	}))

	defaultWorkingDir, err := retryCmd.Flags().GetString("default-working-dir")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/dags", defaultWorkingDir)
}
