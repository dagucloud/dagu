// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrunstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/config"
)

func TestNewPostgresRequiresRoleSpecificDSN(t *testing.T) {
	cfg := &config.Config{
		ControlPlaneStore: config.ControlPlaneStoreConfig{
			Backend: config.ControlPlaneStoreBackendPostgres,
			Postgres: config.ControlPlaneStorePostgresConfig{
				Server: config.ControlPlaneStorePostgresRoleConfig{
					DSN: "postgres://server@example.invalid/dagu",
				},
				Scheduler: config.ControlPlaneStorePostgresRoleConfig{
					DSN: "postgres://scheduler@example.invalid/dagu",
				},
				Agent: config.ControlPlaneStorePostgresRoleConfig{
					DirectAccess: true,
				},
			},
		},
	}

	_, err := New(context.Background(), cfg, WithRole(RoleAgent))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "control_plane_store.postgres.agent.dsn is required")
}

func TestNewPostgresRejectsAgentDirectAccessByDefault(t *testing.T) {
	cfg := &config.Config{
		ControlPlaneStore: config.ControlPlaneStoreConfig{
			Backend: config.ControlPlaneStoreBackendPostgres,
			Postgres: config.ControlPlaneStorePostgresConfig{
				Agent: config.ControlPlaneStorePostgresRoleConfig{
					DSN: "postgres://agent@example.invalid/dagu",
				},
			},
		},
	}

	_, err := New(context.Background(), cfg, WithRole(RoleAgent))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "control_plane_store.postgres.agent.direct_access must be true")
}
