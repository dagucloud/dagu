// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/stretchr/testify/require"
)

func TestApplyRunOverridesMergesLabels(t *testing.T) {
	dag := &core.DAG{
		Labels: core.NewLabels([]string{"env=prod"}),
	}

	applyRunOverrides(dag, RunOptions{
		Labels: []string{"env=prod", "team=platform"},
	})

	require.Equal(t, []string{"env=prod", "team=platform"}, dag.Labels.Strings())
}
