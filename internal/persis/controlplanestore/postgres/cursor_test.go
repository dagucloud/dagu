// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core/exec"
)

func TestQueryFilterHashIncludesWorkspaceFilter(t *testing.T) {
	base := exec.ListDAGRunStatusesOptions{Name: "example"}

	hashWithoutFilter := queryFilterHash(base)
	hashWithOps := queryFilterHash(exec.ListDAGRunStatusesOptions{
		Name: "example",
		WorkspaceFilter: &exec.WorkspaceFilter{
			Enabled:           true,
			Workspaces:        []string{"ops"},
			IncludeUnlabelled: false,
		},
	})
	hashWithPlatform := queryFilterHash(exec.ListDAGRunStatusesOptions{
		Name: "example",
		WorkspaceFilter: &exec.WorkspaceFilter{
			Enabled:           true,
			Workspaces:        []string{"platform"},
			IncludeUnlabelled: false,
		},
	})

	require.NotEqual(t, hashWithoutFilter, hashWithOps)
	assert.NotEqual(t, hashWithOps, hashWithPlatform)
}

func TestQueryFilterHashSortsWorkspaceFilter(t *testing.T) {
	first := queryFilterHash(exec.ListDAGRunStatusesOptions{
		WorkspaceFilter: &exec.WorkspaceFilter{
			Enabled:    true,
			Workspaces: []string{"platform", "ops"},
		},
	})
	second := queryFilterHash(exec.ListDAGRunStatusesOptions{
		WorkspaceFilter: &exec.WorkspaceFilter{
			Enabled:    true,
			Workspaces: []string{"ops", "platform"},
		},
	})

	assert.Equal(t, first, second)
}
