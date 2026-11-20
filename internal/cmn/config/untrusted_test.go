// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Without the block, there is no restricted user, and isolation is not turned off.
func TestUntrustedDefault(t *testing.T) {
	cfg := loadFromYAML(t, "debug: false\n")
	assert.Equal(t, UntrustedConfig{}, cfg.Untrusted)
	assert.Empty(t, isolationWarnings(cfg))
}

// isolationWarnings returns the warnings that name the key untrusted.isolation.
func isolationWarnings(cfg *Config) []string {
	var found []string
	for _, w := range cfg.Warnings {
		if strings.Contains(w, "untrusted.isolation") {
			found = append(found, w)
		}
	}
	return found
}

// The key isolation turns the isolation off, and the start logs a warning that
// names the key. The value on keeps the default.
func TestUntrustedIsolation(t *testing.T) {
	cfg := loadFromYAML(t, "untrusted:\n  isolation: off\n")
	assert.Equal(t, UntrustedConfig{IsolationOff: true}, cfg.Untrusted)
	assert.Len(t, isolationWarnings(cfg), 1)

	cfg = loadFromYAML(t, "untrusted:\n  isolation: on\n")
	assert.Equal(t, UntrustedConfig{}, cfg.Untrusted)
	assert.Empty(t, isolationWarnings(cfg))

	cfg = loadFromYAML(t, "untrusted:\n  isolation: on\n  uid: 10001\n  gid: 10002\n")
	assert.Equal(t, UntrustedConfig{Enabled: true, UID: 10001, GID: 10002}, cfg.Untrusted)
}

// The blocks ai, untrusted and agent_tools are file only. A variable that names
// a whole block stops Dagu at start. The error names the variable and the key,
// and never repeats the value.
func TestFileOnlyBlockVariablesStopStart(t *testing.T) {
	for _, tc := range []struct{ variable, block string }{
		{"DAGU_AI", "ai"},
		{"DAGU_UNTRUSTED", "untrusted"},
		{"DAGU_AGENT_TOOLS", "agent_tools"},
	} {
		t.Run(tc.variable, func(t *testing.T) {
			t.Setenv(tc.variable, "super-secret-value")
			err := testLoadWithError(t)
			require.Error(t, err)
			assert.Equal(t, "config: the environment variable "+tc.variable+" is not read: set the block "+tc.block+" in the configuration file", err.Error())
			assert.NotContains(t, err.Error(), "super-secret-value")
		})
	}
}

// The file sets both keys.
func TestUntrustedFromFile(t *testing.T) {
	cfg := loadFromYAML(t, "untrusted:\n  uid: 10001\n  gid: 10002\n")
	assert.Equal(t, UntrustedConfig{Enabled: true, UID: 10001, GID: 10002}, cfg.Untrusted)
}

// No environment variable sets a key of the block.
func TestUntrustedHasNoEnvironmentVariable(t *testing.T) {
	t.Setenv("DAGU_UNTRUSTED_UID", "10001")
	t.Setenv("DAGU_UNTRUSTED_GID", "10001")
	cfg := loadFromYAML(t, "debug: false\n")
	assert.False(t, cfg.Untrusted.Enabled)
}

// A wrong value stops Dagu at start. The error names the key, the expected
// range and the value, and no value is changed to make it fit.
func TestUntrustedErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want []string
	}{
		{"uid without gid", "untrusted:\n  uid: 10001\n", []string{`"untrusted.gid" is required with "untrusted.uid"`}},
		{"gid without uid", "untrusted:\n  gid: 10001\n", []string{`"untrusted.uid" is required with "untrusted.gid"`}},
		{"zero", "untrusted:\n  uid: 0\n  gid: 10001\n", []string{`"untrusted.uid"`, "from 1 to 4294967294", "got 0"}},
		{"negative", "untrusted:\n  uid: 10001\n  gid: -1\n", []string{`"untrusted.gid"`, "from 1 to 4294967294", "got -1"}},
		{"too large", "untrusted:\n  uid: 4294967295\n  gid: 10001\n", []string{`"untrusted.uid"`, "from 1 to 4294967294", "4294967295"}},
		{"string", "untrusted:\n  uid: \"10001\"\n  gid: 10001\n", []string{`"untrusted.uid"`, `got "10001"`}},
		{"fraction", "untrusted:\n  uid: 10001.5\n  gid: 10001\n", []string{`"untrusted.uid"`, "10001.5"}},
		{"integral float", "untrusted:\n  uid: 10001.0\n  gid: 10001\n", []string{`"untrusted.uid"`, "float64"}},
		{"bool", "untrusted:\n  uid: true\n  gid: 10001\n", []string{`"untrusted.uid"`, "got true"}},
		{"unknown key", "untrusted:\n  uid: 10001\n  gid: 10001\n  groups: [1]\n", []string{`"untrusted.groups"`, "valid keys are gid, isolation, uid"}},
		{"isolation off with uid and gid", "untrusted:\n  isolation: off\n  uid: 10001\n  gid: 10001\n", []string{`"untrusted.isolation"`, `"off"`, `"untrusted.uid"`, `"untrusted.gid"`}},
		{"isolation unknown value", "untrusted:\n  isolation: maybe\n", []string{`"untrusted.isolation"`, `expected "on" or "off"`, `got "maybe"`}},
		{"isolation boolean", "untrusted:\n  isolation: false\n", []string{`"untrusted.isolation"`, `expected "on" or "off"`, "got false"}},
		{"isolation quoted case", "untrusted:\n  isolation: \"Off\"\n", []string{`"untrusted.isolation"`, `got "Off"`}},
		{"block is not an object", "untrusted: 10001\n", []string{`"untrusted"`, "expected an object"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := loadWithErrorFromYAML(t, tc.yaml)
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}

	t.Run("duplicate key", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(file, []byte("untrusted:\n  uid: 10001\n  uid: 10002\n  gid: 10001\n"), 0o600))
		_, err := NewConfigLoader(viper.New(), WithAppHomeDir(t.TempDir()), WithConfigFile(file)).Load()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uid")
	})
}

// The restricted user cannot have the IDs of the process.
func TestUntrustedNotTheProcessIDs(t *testing.T) {
	t.Parallel()

	_, err := ParseUntrustedConfig(map[string]any{"uid": 1000, "gid": 2000}, []int{1000}, []int{3000})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"untrusted.uid" is set to 1000, which is the user ID of the process`)

	_, err = ParseUntrustedConfig(map[string]any{"uid": 1000, "gid": 2000}, []int{5}, []int{2000})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"untrusted.gid" is set to 2000, which is the group ID of the process`)

	parsed, err := ParseUntrustedConfig(map[string]any{"uid": uint64(4294967294), "gid": int64(1)}, []int{0}, []int{0})
	require.NoError(t, err)
	assert.Equal(t, UntrustedConfig{Enabled: true, UID: 4294967294, GID: 1}, parsed)
}
