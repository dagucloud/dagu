// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
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

// A test run installs the declared tools only when it has a real step. The
// installer refuses the provider, so its error shows that it ran.
func TestPrepareTestDAGTools(t *testing.T) {
	t.Parallel()

	ctx := &Context{
		Context: context.Background(),
		Config: &config.Config{Paths: config.PathsConfig{
			ToolsDir: t.TempDir(),
			DataDir:  t.TempDir(),
		}},
	}
	dag := &ir.DAG{Tools: &ir.ToolConfig{Provider: "unknown"}}

	envs, err := prepareTestDAGTools(ctx, dag, false)
	require.NoError(t, err)
	assert.Empty(t, envs)

	_, err = prepareTestDAGTools(ctx, dag, true)
	require.ErrorContains(t, err, `unsupported tools provider "unknown"`)

	// A DAG that cannot use tools fails as in a real run.
	dag.Container = &ir.Container{Image: "alpine"}
	_, err = prepareTestDAGTools(ctx, dag, false)
	require.ErrorContains(t, err, "container")
}
