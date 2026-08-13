// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"testing"

	generated "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

type stubBaseConfigStore struct {
	spec string
}

func (s stubBaseConfigStore) GetSpec(context.Context) (string, error) {
	return s.spec, nil
}

func (stubBaseConfigStore) UpdateSpec(context.Context, []byte) error {
	return nil
}

type workspaceStoreStub struct {
	workspace.Store
	item *workspace.Workspace
}

func (s workspaceStoreStub) GetByName(context.Context, string) (*workspace.Workspace, error) {
	return s.item, nil
}

func TestRequireBaseConfigManagementRequiresWorkspaceProvider(t *testing.T) {
	t.Parallel()

	a := &API{baseConfigStore: stubBaseConfigStore{}}

	assert.ErrorIs(t, a.requireBaseConfigManagement(), ErrBaseConfigNotAvailable)

	a.baseConfigProvider = func(string) (dagsettings.BaseConfigStore, error) {
		return stubBaseConfigStore{}, nil
	}
	require.NoError(t, a.requireBaseConfigManagement())
}

func TestNewPanicsWhenBaseConfigStoreHasNoWorkspaceProvider(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"api: workspace base config provider must be configured when base config store is configured",
		func() {
			New(
				nil,
				nil,
				nil,
				nil,
				runtime.Manager{},
				&config.Config{},
				nil,
				nil,
				nil,
				nil,
				WithBaseConfigStore(stubBaseConfigStore{}),
			)
		},
	)
}

func TestGetWorkspaceBaseConfigUsesProvider(t *testing.T) {
	t.Parallel()

	a := &API{
		baseConfigStore: stubBaseConfigStore{},
		baseConfigProvider: func(name string) (dagsettings.BaseConfigStore, error) {
			assert.Equal(t, "operations", name)
			return stubBaseConfigStore{spec: "max_active_runs: 2\n"}, nil
		},
		workspaceStore: workspaceStoreStub{
			item: &workspace.Workspace{Name: "operations"},
		},
	}

	response, err := a.GetWorkspaceBaseConfig(context.Background(), generated.GetWorkspaceBaseConfigRequestObject{
		WorkspaceName: "operations",
	})
	require.NoError(t, err)
	assert.Equal(t, "max_active_runs: 2\n", response.(generated.GetWorkspaceBaseConfig200JSONResponse).Spec)
}
