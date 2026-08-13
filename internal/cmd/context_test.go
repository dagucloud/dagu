// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestContext_StringParam(t *testing.T) {
	tests := []struct {
		name      string
		flagName  string
		flagValue string
		expected  string
		expectErr bool
	}{
		{
			name:      "StringParamWithoutQuotes",
			flagName:  "test-param",
			flagValue: "hello",
			expected:  "hello",
			expectErr: false,
		},
		{
			name:      "StringParamWithDoubleQuotes",
			flagName:  "test-param",
			flagValue: `"world"`,
			expected:  "world",
			expectErr: false,
		},
		{
			name:      "EmptyStringParam",
			flagName:  "test-param",
			flagValue: `""`,
			expected:  "",
			expectErr: false,
		},
		{
			name:      "StringParamWithEscapedDoubleQuotes",
			flagName:  "test-param",
			flagValue: `"{\"key\":\"value with \\\"quotes\\\"\"}"`, // This is the string literal `{"key":"value with \"quotes\""}`
			expected:  `{"key":"value with \"quotes\""}`,
			expectErr: false,
		},
		{
			name:      "JSONStringParam",
			flagName:  "test-param",
			flagValue: `"{ \"name\": \"test\", \"value\": 123 }"`, // This is the string literal `{ "name": "test", "value": 123 }`
			expected:  `{ "name": "test", "value": 123 }`,
			expectErr: false,
		},
		{
			name:      "FlagNotFound",
			flagName:  "non-existent-param",
			flagValue: "", // Value doesn't matter if flag not found
			expected:  "",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{
				Use: "test",
			}
			if tt.flagName != "non-existent-param" { // Only add flag if it's expected to exist
				cmd.Flags().String(tt.flagName, "", "test flag")
				_ = cmd.Flags().Set(tt.flagName, tt.flagValue)
			}

			ctx := &Context{
				Command: cmd,
			}

			val, err := ctx.StringParam(tt.flagName)

			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected an error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Did not expect an error but got: %v", err)
				}
				if val != tt.expected {
					t.Errorf("Expected %q, got %q", tt.expected, val)
				}
			}
		})
	}
}

func TestNewFileStoresAllowsUnavailableEventStorageForCommands(t *testing.T) {
	t.Parallel()

	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("not a directory"), 0o600))
	cfg := &config.Config{
		EventStore: config.EventStoreConfig{Enabled: true},
		Paths:      config.PathsConfig{EventStoreDir: filepath.Join(blocker, "events")},
	}

	stores, _, err := newFileStores(t.Context(), cfg, "status")
	require.NoError(t, err)
	require.Nil(t, stores.Event)
}

func TestNewFileStoresRequiresDAGSettingsStorageForStartAll(t *testing.T) {
	t.Parallel()

	_, _, err := newFileStores(t.Context(), &config.Config{}, "start-all")
	require.ErrorContains(t, err, "failed to initialize DAG settings store")
}

func TestNewCoordinatorClientRejectsInvalidEnabledConfig(t *testing.T) {
	t.Parallel()

	ctx := &Context{Config: &config.Config{Coordinator: config.Coordinator{Enabled: true}}}

	client, err := ctx.NewCoordinatorClient()
	require.Nil(t, client)
	require.ErrorContains(t, err, "invalid coordinator client configuration")
}
