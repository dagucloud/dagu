// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The block agent_tools has the defaults of Spec 090, each value says that it is a
// default, and an environment variable does not set a key of the block.
func TestAgentToolsDefaults(t *testing.T) {
	t.Setenv("DAGU_AGENT_TOOLS_MAX_TOOLS", "3")
	t.Setenv("DAGU_AGENT_TOOLS_TOOL_RESULT_MAX_BYTES", "3")
	cfg := testLoad(t)
	tools := cfg.AgentTools
	assert.Equal(t, 64, tools.MaxTools)
	assert.Equal(t, 524288, tools.ToolResultMaxBytes)
	for _, doc := range AgentToolsKeyDocs {
		assert.Equal(t, "default", tools.Sources[doc.Key], doc.Key)
	}
}

// The file sets each key, and the source of the value is the file. Zero is a value
// of tool_result_max_bytes: it means no limit.
func TestAgentToolsFromFile(t *testing.T) {
	cfg := loadFromYAML(t, "agent_tools:\n  max_tools: 8\n  tool_result_max_bytes: 0\n")
	assert.Equal(t, 8, cfg.AgentTools.MaxTools)
	assert.Equal(t, 0, cfg.AgentTools.ToolResultMaxBytes)
	for _, doc := range AgentToolsKeyDocs {
		assert.Equal(t, "file", cfg.AgentTools.Sources[doc.Key], doc.Key)
	}
	maxTools, resultMax := cfg.AgentTools.Limits()
	assert.Equal(t, 8, maxTools)
	assert.Equal(t, 0, resultMax)
}

// A Config that the loader did not build gives the defaults.
func TestAgentToolsLimitsOfAnEmptyConfig(t *testing.T) {
	t.Parallel()

	maxTools, resultMax := AgentToolsConfig{}.Limits()
	assert.Equal(t, DefaultAgentToolsMaxTools, maxTools)
	assert.Equal(t, DefaultAgentToolsToolResultMaxBytes, resultMax)
}

// The block is closed and strict: an unknown key, a duplicate key, a wrong type and
// a value out of range stop Dagu at start. The error names the key, the expected
// type and the value, and no value is changed to make it fit.
func TestAgentToolsErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want []string
	}{
		{"unknown key", "agent_tools:\n  max_tool: 3\n", []string{`"agent_tools.max_tool"`, "valid keys are max_tools, tool_result_max_bytes"}},
		{"string for integer", "agent_tools:\n  max_tools: \"8\"\n", []string{`"agent_tools.max_tools"`, "an integer of at least 1", `got "8"`}},
		{"zero tools", "agent_tools:\n  max_tools: 0\n", []string{`"agent_tools.max_tools"`, "at least 1", "got 0"}},
		{"negative tools", "agent_tools:\n  max_tools: -4\n", []string{`"agent_tools.max_tools"`, "got -4"}},
		{"negative result", "agent_tools:\n  tool_result_max_bytes: -1\n", []string{`"agent_tools.tool_result_max_bytes"`, "at least 0", "got -1"}},
		{"fraction", "agent_tools:\n  max_tools: 2.5\n", []string{`"agent_tools.max_tools"`, "2.5"}},
		{"integral float", "agent_tools:\n  max_tools: 3.0\n", []string{`"agent_tools.max_tools"`, "float64"}},
		{"bool for integer", "agent_tools:\n  tool_result_max_bytes: true\n", []string{`"agent_tools.tool_result_max_bytes"`, "got true"}},
		{"block is not an object", "agent_tools: yes\n", []string{`"agent_tools"`, "expected an object"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "config.yaml")
			require.NoError(t, os.WriteFile(file, []byte(tc.yaml), 0o600))
			_, err := NewConfigLoader(viper.New(), WithAppHomeDir(t.TempDir()), WithConfigFile(file)).Load()
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}

	t.Run("duplicate key", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "config.yaml")
		require.NoError(t, os.WriteFile(file, []byte("agent_tools:\n  max_tools: 2\n  max_tools: 3\n"), 0o600))
		_, err := NewConfigLoader(viper.New(), WithAppHomeDir(t.TempDir()), WithConfigFile(file)).Load()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max_tools")
	})
}

// The reference of the keys lists each key once, with its default.
func TestAgentToolsKeyDocs(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, doc := range AgentToolsKeyDocs {
		assert.False(t, seen[doc.Key], doc.Key)
		seen[doc.Key] = true
		assert.NotEmpty(t, doc.Default)
		assert.NotEmpty(t, doc.Description)
		_, known := agentToolsKeys[doc.Key]
		assert.True(t, known, doc.Key)
	}
	assert.Len(t, agentToolsKeys, len(AgentToolsKeyDocs))
	parsed, err := ParseAgentToolsConfig(nil)
	require.NoError(t, err)
	assert.Len(t, parsed.Sources, len(AgentToolsKeyDocs))
}
