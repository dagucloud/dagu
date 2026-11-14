// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudaccess

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
)

func TestSettingsStartOffAndKeepChoices(t *testing.T) {
	ctx := context.Background()
	store := filemonitor.NewStateStore(filepath.Join(t.TempDir(), "access-settings.json"))
	settings := NewSettings(config.CloudConfig{}, store)
	status, err := settings.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, Status{Level: config.RemoteAccessOff}, status)

	require.NoError(t, settings.Set(ctx, new(config.RemoteAccessOperator), nil))
	require.NoError(t, settings.Set(ctx, nil, new(true)))
	status, err = settings.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, Status{Level: config.RemoteAccessOperator, Approvals: true}, status)
	require.ErrorIs(t, settings.Set(ctx, new(config.RemoteAccessLevel("admin")), nil), ErrUnknownLevel)

	// Another process sharing the data directory sees the choices, and this
	// one sees its changes once what it read is older than settingsTTL.
	other := NewSettings(config.CloudConfig{}, store)
	status, err = other.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, config.RemoteAccessOperator, status.Level)
	require.NoError(t, other.Set(ctx, new(config.RemoteAccessOff), nil))
	status, err = settings.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, config.RemoteAccessOperator, status.Level)
	settings.now = func() time.Time { return time.Now().Add(settingsTTL) }
	status, err = settings.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, Status{Level: config.RemoteAccessOff, Approvals: true}, status)
}

func TestConfigurationFixesSettings(t *testing.T) {
	ctx := context.Background()
	store := filemonitor.NewStateStore(filepath.Join(t.TempDir(), "access-settings.json"))
	settings := NewSettings(config.CloudConfig{RemoteAccess: config.RemoteAccessViewer, RemoteApprovals: new(false)}, store)
	require.ErrorIs(t, settings.Set(ctx, new(config.RemoteAccessDeveloper), nil), ErrConfigured)
	require.ErrorIs(t, settings.Set(ctx, nil, new(true)), ErrConfigured)
	status, err := settings.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, Status{Level: config.RemoteAccessViewer, LevelConfigured: true, ApprovalsConfigured: true}, status)

	// A change that configuration refuses in part changes nothing.
	settings = NewSettings(config.CloudConfig{RemoteApprovals: new(false)}, store)
	require.ErrorIs(t, settings.Set(ctx, new(config.RemoteAccessOperator), new(true)), ErrConfigured)
	status, err = settings.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, config.RemoteAccessOff, status.Level)

	// Configuration fixing the level leaves approvals to choose.
	settings = NewSettings(config.CloudConfig{RemoteAccess: config.RemoteAccessViewer}, store)
	require.NoError(t, settings.Set(ctx, nil, new(true)))
	status, err = settings.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, Status{Level: config.RemoteAccessViewer, LevelConfigured: true, Approvals: true}, status)
}
