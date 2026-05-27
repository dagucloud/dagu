// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentsnapshot

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/require"
)

func TestBuildFromPaths_SkipsStoreInitForNonAgentDAG(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
		Name: "non-agent",
		Steps: []core.Step{
			{Name: "main"},
		},
	}

	snapshot, err := BuildFromPaths(context.Background(), dag, config.PathsConfig{}, nil, nil)
	require.NoError(t, err)
	require.Nil(t, snapshot)
}

func TestBuildFromPaths_RequiresConfiguredStoresForAgentDAG(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
		Name: "agent",
		Steps: []core.Step{
			{
				Name:  "agent-step",
				Agent: &core.AgentStepConfig{},
			},
		},
	}

	snapshot, err := BuildFromPaths(context.Background(), dag, config.PathsConfig{}, nil, nil)
	require.Nil(t, snapshot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "snapshot store factory is not configured")
}
