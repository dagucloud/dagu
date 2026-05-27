// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/baseconfig"
	"github.com/dagucloud/dagu/internal/runtime"
)

type stubBaseConfigStore struct{}

func (stubBaseConfigStore) GetSpec(context.Context) (string, error) {
	return "", nil
}

func (stubBaseConfigStore) UpdateSpec(context.Context, []byte) error {
	return nil
}

func TestRequireBaseConfigManagementRequiresWorkspaceFactory(t *testing.T) {
	t.Parallel()

	a := &API{baseConfigStore: stubBaseConfigStore{}}

	assert.ErrorIs(t, a.requireBaseConfigManagement(), ErrBaseConfigNotAvailable)

	a.baseConfigFactory = func(string, string) (baseconfig.Store, error) {
		return stubBaseConfigStore{}, nil
	}
	require.NoError(t, a.requireBaseConfigManagement())
}

func TestNewPanicsWhenBaseConfigStoreHasNoWorkspaceFactory(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"api: workspace base config store factory must be configured when base config store is configured",
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
