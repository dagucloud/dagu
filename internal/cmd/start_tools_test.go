// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	dagutools "github.com/dagucloud/dagu/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateDAGToolsSupportedRejectsContainer(t *testing.T) {
	t.Parallel()

	err := dagutools.ValidateDAGSupported(&core.DAG{
		Tools:     &core.ToolConfig{Provider: "aqua"},
		Container: &core.Container{Image: "alpine"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "container")
}

func TestValidateDAGToolsSupportedAllowsHostCommandSteps(t *testing.T) {
	t.Parallel()

	err := dagutools.ValidateDAGSupported(&core.DAG{
		Tools: &core.ToolConfig{Provider: "aqua"},
		Steps: []core.Step{{
			Name: "check",
			Commands: []core.CommandEntry{{
				Command: "jq",
			}},
		}},
	})

	require.NoError(t, err)
}

func TestDAGToolsBasePathUsesConfiguredBaseEnv(t *testing.T) {
	t.Parallel()

	ctx := &Context{
		Config: &config.Config{
			Core: config.Core{
				BaseEnv: config.NewBaseEnv([]string{"PATH=/configured/bin"}),
			},
		},
	}

	assert.Equal(t, "/configured/bin", dagToolsBasePath(ctx))
}
