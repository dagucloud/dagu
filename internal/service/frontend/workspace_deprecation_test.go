// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/workspace/deprecation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWarnWorkspaceDeprecation(t *testing.T) {
	t.Run("OneWarningNamesEverySetting", func(t *testing.T) {
		var output bytes.Buffer
		srv := &Server{workspaceDeprecation: deprecation.Sources{ConfigKeys: []string{
			"auth.oidc.role_mapping.workspace_mappings",
			"auth.proxy.role_mapping.default_workspace_access",
		}}}

		srv.warnWorkspaceDeprecation(jsonLogContext(&output))

		lines := strings.Split(strings.TrimSpace(output.String()), "\n")
		require.Len(t, lines, 1)
		var entry struct {
			Level    string `json:"level"`
			Msg      string `json:"msg"`
			Settings string `json:"settings"`
		}
		require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry))
		assert.Equal(t, "WARN", entry.Level)
		assert.Equal(t, deprecation.Warning, entry.Msg)
		assert.Equal(t,
			"config key auth.oidc.role_mapping.workspace_mappings; config key auth.proxy.role_mapping.default_workspace_access",
			entry.Settings)
	})

	t.Run("SilentWithoutSettings", func(t *testing.T) {
		var output bytes.Buffer
		srv := &Server{workspaceDeprecation: deprecation.Sources{DAGsDir: t.TempDir()}}

		srv.warnWorkspaceDeprecation(jsonLogContext(&output))

		assert.Empty(t, output.String())
	})
}

func jsonLogContext(output *bytes.Buffer) context.Context {
	return logger.WithLogger(context.Background(), logger.NewLogger(
		logger.WithWriter(output),
		logger.WithFormat("json"),
		logger.WithQuiet(),
	))
}
