// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sshconn

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient_WithBastionConfig(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		User:     "testuser",
		Host:     "target.example.com",
		Port:     "22",
		Password: "targetpass",
		Bastion: &BastionConfig{
			Host:     "bastion.example.com",
			Port:     "2222",
			User:     "bastionuser",
			Password: "bastionpass",
		},
	}

	client, err := NewClient(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Verify bastion config is properly set
	assert.NotNil(t, client.bastionCfg)
	assert.Equal(t, "bastion.example.com:2222", client.bastionCfg.hostPort)
}

func TestDefaultIfZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		val        time.Duration
		defaultVal time.Duration
		expected   time.Duration
	}{
		{
			name:       "ZeroValue",
			val:        0,
			defaultVal: 30 * time.Second,
			expected:   30 * time.Second,
		},
		{
			name:       "NonZeroValue",
			val:        1 * time.Minute,
			defaultVal: 30 * time.Second,
			expected:   1 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := defaultIfZero(tt.val, tt.defaultVal)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDefaultIfEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		val        string
		defaultVal string
		expected   string
	}{
		{
			name:       "EmptyValue",
			val:        "",
			defaultVal: "22",
			expected:   "22",
		},
		{
			name:       "ZeroString",
			val:        "0",
			defaultVal: "22",
			expected:   "22",
		},
		{
			name:       "NonEmptyValue",
			val:        "2222",
			defaultVal: "22",
			expected:   "2222",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := defaultIfEmpty(tt.val, tt.defaultVal)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetDefaultSSHKeys(t *testing.T) {
	t.Parallel()

	keys := getDefaultSSHKeys()

	// Should return 4 default key paths
	assert.Len(t, keys, 4)

	// All paths should contain .ssh directory
	for _, key := range keys {
		assert.Contains(t, key, ".ssh")
	}

	// Should contain the standard key names
	keyNames := strings.Join(keys, ",")
	assert.Contains(t, keyNames, "id_rsa")
	assert.Contains(t, keyNames, "id_ecdsa")
	assert.Contains(t, keyNames, "id_ed25519")
	assert.Contains(t, keyNames, "id_dsa")
}

func TestGetHostKeyCallback_InsecureMode(t *testing.T) {
	t.Parallel()

	// When strict_host_key is false, should return InsecureIgnoreHostKey
	callback, err := getHostKeyCallback(false, "")
	require.NoError(t, err)
	require.NotNil(t, callback)

	// The callback should accept any host key (insecure mode)
	// We can't easily test the callback itself, but we verify it's not nil
}

func TestSelectSSHAuthMethod_Password(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Password: "testpassword",
	}

	authMethod, err := selectSSHAuthMethod(context.Background(), cfg.SSHAuth)
	require.NoError(t, err)
	require.NotNil(t, authMethod)
}

func TestSelectSSHAuthMethod_NoAuth(t *testing.T) {
	t.Parallel()

	// Skip if default SSH keys exist on the system
	if findDefaultSSHKey() != "" {
		t.Skip("Skipping: default SSH keys found on system")
	}

	// No key, no password, and no default keys exist
	cfg := &Config{
		// Empty - no auth method specified
	}

	_, err := selectSSHAuthMethod(context.Background(), cfg.SSHAuth)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no SSH key specified")
}

func TestSelectBastionAuthMethod_Password(t *testing.T) {
	t.Parallel()

	bastion := &BastionConfig{
		Host:     "bastion.example.com",
		User:     "bastionuser",
		Password: "bastionpass",
	}

	authMethod, err := selectBastionAuthMethod(context.Background(), bastion.SSHAuth)
	require.NoError(t, err)
	require.NotNil(t, authMethod)
}

func TestSelectBastionAuthMethod_NoAuth(t *testing.T) {
	t.Parallel()

	// Skip if default SSH keys exist on the system
	if findDefaultSSHKey() != "" {
		t.Skip("Skipping: default SSH keys found on system")
	}

	bastion := &BastionConfig{
		Host: "bastion.example.com",
		User: "bastionuser",
		// No key, no password
	}

	_, err := selectBastionAuthMethod(context.Background(), bastion.SSHAuth)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authentication method available for bastion")
}
