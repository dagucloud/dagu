// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

	t.Run("ShowsTheUntrustedBlock", func(t *testing.T) {
		out := runConfigCmd(t)
		assert.Contains(t, out, "Untrusted (key, value that applies, source):")
		assert.Contains(t, out, "untrusted.uid:")
		assert.Contains(t, out, "untrusted.gid:")
		assert.Contains(t, out, "not set")

		file := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(file, []byte("untrusted:\n  uid: 10001\n  gid: 10002\n"), 0o600))
		root := &cobra.Command{Use: "root"}
		root.AddCommand(cmd.Config())
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetArgs([]string{"config", "--config", file})
		require.NoError(t, root.ExecuteContext(th.Context))
		assert.Regexp(t, `untrusted\.uid:\s+10001\s+file`, buf.String())
		assert.Regexp(t, `untrusted\.gid:\s+10002\s+file`, buf.String())
	})

	t.Run("ShowsTheAgentToolsBlock", func(t *testing.T) {
		out := runConfigCmd(t)
		assert.Contains(t, out, "Agent tools (key, value that applies, source):")
		for _, want := range []string{
			"agent_tools.max_tools:", "64",
			"agent_tools.tool_result_max_bytes:", "524288", "default",
		} {
			assert.Contains(t, out, want)
		}
	})

	t.Run("ShowsTheAIBlock", func(t *testing.T) {
		out := runConfigCmd(t)
		assert.Contains(t, out, "AI (key, value that applies, source):")
		assert.Regexp(t, `ai\.models_refresh_hours:\s+24\s+default`, out)
		assert.Regexp(t, `ai\.upstream\.proxy_url:\s+\(empty\)\s+default`, out)
		assert.Regexp(t, `ai\.relay\.max_streams:\s+128\s+default`, out)

		file := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(file, []byte("ai:\n  relay:\n    max_streams: 64\n    max_streams_per_run: 4\n"), 0o600))
		root := &cobra.Command{Use: "root"}
		root.AddCommand(cmd.Config())
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetArgs([]string{"config", "--config", file})
		require.NoError(t, root.ExecuteContext(th.Context))
		assert.Regexp(t, `ai\.relay\.max_streams:\s+64\s+file`, buf.String())
		assert.Regexp(t, `ai\.relay\.max_streams_per_run:\s+4\s+file`, buf.String())
		assert.Regexp(t, `ai\.relay\.idle_timeout_sec:\s+300\s+default`, buf.String())
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
