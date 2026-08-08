// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	dagutools "github.com/dagucloud/dagu/v2/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateDAGToolsSupportedRejectsContainer(t *testing.T) {
	t.Parallel()

	err := dagutools.ValidateDAGSupported(&ir.DAG{
		Tools:     &ir.ToolConfig{Provider: "aqua"},
		Container: &ir.Container{Image: "alpine"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "container")
}

func TestValidateDAGToolsSupportedAllowsHostCommandSteps(t *testing.T) {
	t.Parallel()

	err := dagutools.ValidateDAGSupported(&ir.DAG{
		Tools: &ir.ToolConfig{Provider: "aqua"},
		Steps: []ir.Step{{
			Name: "check",
			Commands: []ir.CommandEntry{{
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
