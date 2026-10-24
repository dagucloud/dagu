// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigCommand(t *testing.T) {
	th := test.SetupCommand(t)

	execConfigCmd := func(t *testing.T, args ...string) (string, error) {
		t.Helper()

		root := &cobra.Command{Use: "root"}
		root.AddCommand(cmd.Config())

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetArgs(append([]string{"config", "--config", th.Config.Paths.ConfigFileUsed}, args...))

		err := root.ExecuteContext(th.Context)
		return buf.String(), err
	}

	runConfigCmd := func(t *testing.T, args ...string) string {
		t.Helper()

		out, err := execConfigCmd(t, args...)
		require.NoError(t, err)
		return out
	}

	t.Run("ShowsLabels", func(t *testing.T) {
		out := runConfigCmd(t)
		assert.Contains(t, out, "DAGs directory:")
		assert.Contains(t, out, "Wiki directory:")
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
		assert.Contains(t, out, th.Config.Paths.WikiDir)
		assert.Contains(t, out, th.Config.Paths.DAGRunsDir)
		assert.Contains(t, out, th.Config.Paths.LogDir)
		assert.Contains(t, out, th.Config.Paths.DataDir)
		assert.Contains(t, out, th.Config.Paths.ToolsDir)
	})

	t.Run("ValuesTable", func(t *testing.T) {
		t.Setenv("DAGU_HOST", "0.0.0.0")

		out := runConfigCmd(t, "--values")
		assert.Regexp(t, `(?m)^KEY\s+VALUE\s+SOURCE$`, out)
		assert.Regexp(t, `(?m)^host\s+0\.0\.0\.0\s+env:DAGU_HOST$`, out)
		assert.NotContains(t, out, "DAGs directory:")
	})

	t.Run("ValuesJSON", func(t *testing.T) {
		t.Setenv("DAGU_AUTH_TOKEN_SECRET", "do-not-print")

		out := runConfigCmd(t, "--values", "--format", "json")
		assert.NotContains(t, out, "do-not-print")

		var settings []config.Setting
		require.NoError(t, json.Unmarshal([]byte(out), &settings))
		assert.Contains(t, settings, config.Setting{
			Key:    "auth.builtin.token.secret",
			Value:  config.MaskedValue,
			Source: "env:DAGU_AUTH_TOKEN_SECRET",
		})
	})

	t.Run("FormatRequiresValues", func(t *testing.T) {
		_, err := execConfigCmd(t, "--format", "json")
		require.ErrorContains(t, err, "--format requires --values")
	})

	t.Run("InvalidFormat", func(t *testing.T) {
		_, err := execConfigCmd(t, "--values", "--format", "yaml")
		require.ErrorContains(t, err, "invalid format")
	})
}
