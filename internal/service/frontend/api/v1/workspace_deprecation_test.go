// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/workspace/deprecation"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetWorkspaceDeprecation(t *testing.T) {
	dagsDir := t.TempDir()
	baseConfig := filepath.Join(dagsDir, "workspaces", "billing", "base.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(baseConfig), 0o750))
	require.NoError(t, os.WriteFile(baseConfig, []byte("env:\n  - REGION: eu\n"), 0o600))
	a := apiv1.New(
		nil, nil, nil, nil, runtime.Manager{},
		&config.Config{}, nil, nil,
		prometheus.NewRegistry(),
		nil,
		apiv1.WithAuthService(stubAuthService{}),
		apiv1.WithWorkspaceDeprecation(deprecation.Sources{
			DAGsDir:    dagsDir,
			ConfigKeys: []string{"auth.oidc.role_mapping.workspace_mappings"},
		}),
	)

	t.Run("Admin", func(t *testing.T) {
		resp, err := a.GetWorkspaceDeprecation(adminCtx(), apigen.GetWorkspaceDeprecationRequestObject{})

		require.NoError(t, err)
		assert.Equal(t, apigen.GetWorkspaceDeprecation200JSONResponse{
			Items: []apigen.WorkspaceDeprecationItem{
				{Kind: apigen.WorkspaceDeprecationItemKindBaseConfig, Name: "billing"},
				{Kind: apigen.WorkspaceDeprecationItemKindConfigKey, Name: "auth.oidc.role_mapping.workspace_mappings"},
			},
		}, resp)
	})

	t.Run("NotAdmin", func(t *testing.T) {
		ctx := auth.WithUser(context.Background(), &auth.User{Username: "manager", Role: auth.RoleManager})

		_, err := a.GetWorkspaceDeprecation(ctx, apigen.GetWorkspaceDeprecationRequestObject{})

		require.Error(t, err)
	})
}
