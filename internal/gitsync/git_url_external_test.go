// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/gitsync"
	"github.com/stretchr/testify/require"
)

func TestNormalizeRepoURLKeepsSCPStyleURLWithNonGitUser(t *testing.T) {
	require.Equal(
		t,
		"deploy@example.com:dagucloud/dagu.git",
		gitsync.NormalizeRepoURLForTest("deploy@example.com:dagucloud/dagu.git"),
	)
}

func TestIsSCPStyleURLRequiresPath(t *testing.T) {
	require.False(t, gitsync.IsSCPStyleURLForTest("deploy@example.com:"))
}
