// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigCommand(t *testing.T) {
	th := test.SetupCommand(t)

	runConfigCmd := func(t *testing.T) string {
		t.Helper()

		root := &cobra.Command{Use: "root"}
		root.AddCommand(cmd.Config())

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetArgs([]string{"config", "--config", th.Config.Paths.ConfigFileUsed})

		err := root.ExecuteContext(th.Context)
		require.NoError(t, err)
		return buf.String()
	}

	t.Run("ShowsLabels", func(t *testing.T) {
		out := runConfigCmd(t)
		assert.Contains(t, out, "DAGs directory:")
		assert.Contains(t, out, "Docs directory:")
		assert.Contains(t, out, "DAG runs:")
		assert.Contains(t, out, "Log directory:")
		assert.Contains(t, out, "Data directory:")
		assert.Contains(t, out, "Tools directory:")
		assert.Contains(t, out, "Suspend flags:")
		assert.Contains(t, out, "Queue:")
		assert.Contains(t, out, "Processes:")
	})

	t.Run("ShowsConfiguredPaths", func(t *testing.T) {
		out := runConfigCmd(t)
		assert.Contains(t, out, th.Config.Paths.DAGsDir)
		assert.Contains(t, out, th.Config.Paths.DAGRunsDir)
		assert.Contains(t, out, th.Config.Paths.LogDir)
		assert.Contains(t, out, th.Config.Paths.DataDir)
		assert.Contains(t, out, th.Config.Paths.ToolsDir)
	})
}
