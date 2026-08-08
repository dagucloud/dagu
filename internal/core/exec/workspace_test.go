// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

func TestWorkspaceFilterRejectsInvalidWorkspaceLabels(t *testing.T) {
	t.Parallel()

	filter := &WorkspaceFilter{
		Enabled:           true,
		Workspaces:        []string{"ops"},
		IncludeUnlabelled: true,
	}

	assert.False(t, filter.MatchesLabels(ir.NewLabels([]string{"workspace="})))
	assert.False(t, filter.MatchesLabels(ir.NewLabels([]string{"workspace=bad/name"})))
	assert.False(t, filter.MatchesLabels(ir.NewLabels([]string{"workspace=ops", "workspace=prod"})))
	assert.True(t, filter.MatchesLabels(ir.NewLabels([]string{"team=platform"})))
	assert.True(t, filter.MatchesLabels(ir.NewLabels([]string{"workspace=ops"})))
}
