// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStaticRegistry(t *testing.T) {
	t.Run("valid addresses", func(t *testing.T) {
		addresses := []string{
			"coordinator-1:50055",
			"coordinator-2:50056",
		}

		registry, err := NewStaticRegistry(addresses)
		require.NoError(t, err)
		require.NotNil(t, registry)

		members, err := registry.GetServiceMembers(context.Background(), exec.ServiceNameCoordinator)
		require.NoError(t, err)
		require.Len(t, members, 2)

		assert.Equal(t, "coord-0", members[0].ID)
		assert.Equal(t, "coordinator-1", members[0].Host)
		assert.Equal(t, 50055, members[0].Port)
		assert.Equal(t, exec.ServiceStatusActive, members[0].Status)

		assert.Equal(t, "coord-1", members[1].ID)
		assert.Equal(t, "coordinator-2", members[1].Host)
		assert.Equal(t, 50056, members[1].Port)
	})

	t.Run("address without port uses default", func(t *testing.T) {
		addresses := []string{"coordinator-1"}

		registry, err := NewStaticRegistry(addresses)
		require.NoError(t, err)

		members, err := registry.GetServiceMembers(context.Background(), exec.ServiceNameCoordinator)
		require.NoError(t, err)
		require.Len(t, members, 1)

		assert.Equal(t, "coordinator-1", members[0].Host)
		assert.Equal(t, 50055, members[0].Port) // Default port
	})

	t.Run("ipv6 address with port", func(t *testing.T) {
		addresses := []string{"[fdbd:dc02:1a:e12::31]:50055"}

		registry, err := NewStaticRegistry(addresses)
		require.NoError(t, err)

		members, err := registry.GetServiceMembers(context.Background(), exec.ServiceNameCoordinator)
		require.NoError(t, err)
		require.Len(t, members, 1)

		assert.Equal(t, "fdbd:dc02:1a:e12::31", members[0].Host)
		assert.Equal(t, 50055, members[0].Port)
	})

	t.Run("empty addresses list", func(t *testing.T) {
		addresses := []string{}

		_, err := NewStaticRegistry(addresses)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no valid coordinator addresses provided")
	})

	t.Run("only empty strings", func(t *testing.T) {
		addresses := []string{"", "", ""}

		_, err := NewStaticRegistry(addresses)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no valid coordinator addresses provided")
	})

	t.Run("mixed valid and empty addresses", func(t *testing.T) {
		addresses := []string{"", "coordinator-1:50055", ""}

		registry, err := NewStaticRegistry(addresses)
		require.NoError(t, err)

		members, err := registry.GetServiceMembers(context.Background(), exec.ServiceNameCoordinator)
		require.NoError(t, err)
		require.Len(t, members, 1)

		assert.Equal(t, "coordinator-1", members[0].Host)
	})

	t.Run("invalid port", func(t *testing.T) {
		addresses := []string{"coordinator-1:invalid"}

		_, err := NewStaticRegistry(addresses)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid port")
	})

	t.Run("port out of range", func(t *testing.T) {
		addresses := []string{"coordinator-1:99999"}

		_, err := NewStaticRegistry(addresses)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "port must be between 1 and 65535")
	})

	t.Run("port zero", func(t *testing.T) {
		addresses := []string{"coordinator-1:0"}

		_, err := NewStaticRegistry(addresses)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "port must be between 1 and 65535")
	})

	t.Run("empty host", func(t *testing.T) {
		addresses := []string{":50055"}

		_, err := NewStaticRegistry(addresses)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "host cannot be empty")
	})

	t.Run("too many colons", func(t *testing.T) {
		addresses := []string{"coordinator:50055:extra"}

		_, err := NewStaticRegistry(addresses)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid address format")
	})
}

func TestStaticRegistry_GetServiceMembers(t *testing.T) {
	addresses := []string{"coordinator-1:50055"}
	registry, err := NewStaticRegistry(addresses)
	require.NoError(t, err)

	t.Run("coordinator service", func(t *testing.T) {
		members, err := registry.GetServiceMembers(context.Background(), exec.ServiceNameCoordinator)
		require.NoError(t, err)
		require.Len(t, members, 1)
		assert.Equal(t, "coordinator-1", members[0].Host)
	})

	t.Run("other services return empty", func(t *testing.T) {
		members, err := registry.GetServiceMembers(context.Background(), exec.ServiceNameScheduler)
		require.NoError(t, err)
		assert.Empty(t, members)
	})
}

func TestStaticRegistry_NoOps(t *testing.T) {
	addresses := []string{"coordinator-1:50055"}
	registry, err := NewStaticRegistry(addresses)
	require.NoError(t, err)

	t.Run("register is no-op", func(t *testing.T) {
		err := registry.Register(context.Background(), exec.ServiceNameCoordinator, exec.HostInfo{})
		assert.NoError(t, err)
	})

	t.Run("unregister is no-op", func(_ *testing.T) {
		// Should not panic
		registry.Unregister(context.Background())
	})

	t.Run("update status is no-op", func(t *testing.T) {
		err := registry.UpdateStatus(context.Background(), exec.ServiceNameCoordinator, exec.ServiceStatusActive)
		assert.NoError(t, err)
	})
}

func TestParseAddress(t *testing.T) {
	tests := []struct {
		name        string
		addr        string
		wantHost    string
		wantPort    int
		expectError bool
		errorMsg    string
	}{
		{
			name:     "host:port",
			addr:     "coordinator-1:50055",
			wantHost: "coordinator-1",
			wantPort: 50055,
		},
		{
			name:     "host only uses default port",
			addr:     "coordinator-1",
			wantHost: "coordinator-1",
			wantPort: 50055,
		},
		{
			name:     "ip:port",
			addr:     "192.168.1.100:8080",
			wantHost: "192.168.1.100",
			wantPort: 8080,
		},
		{
			name:     "localhost",
			addr:     "localhost:50055",
			wantHost: "localhost",
			wantPort: 50055,
		},
		{
			name:     "ipv6 with port",
			addr:     "[fdbd:dc02:1a:e12::31]:50055",
			wantHost: "fdbd:dc02:1a:e12::31",
			wantPort: 50055,
		},
		{
			name:     "ipv6 without port uses default port",
			addr:     "fdbd:dc02:1a:e12::31",
			wantHost: "fdbd:dc02:1a:e12::31",
			wantPort: 50055,
		},
		{
			name:     "bracketed ipv6 without port uses default port",
			addr:     "[fdbd:dc02:1a:e12::31]",
			wantHost: "fdbd:dc02:1a:e12::31",
			wantPort: 50055,
		},
		{
			name:        "empty host",
			addr:        ":50055",
			expectError: true,
			errorMsg:    "host cannot be empty",
		},
		{
			name:        "invalid port",
			addr:        "host:abc",
			expectError: true,
			errorMsg:    "invalid port",
		},
		{
			name:        "port too high",
			addr:        "host:70000",
			expectError: true,
			errorMsg:    "port must be between 1 and 65535",
		},
		{
			name:        "port negative",
			addr:        "host:-1",
			expectError: true,
			errorMsg:    "port must be between 1 and 65535",
		},
		{
			name:        "too many colons",
			addr:        "host:8080:extra",
			expectError: true,
			errorMsg:    "invalid address format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, err := parseAddress(tt.addr)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantHost, host)
				assert.Equal(t, tt.wantPort, port)
			}
		})
	}
}
