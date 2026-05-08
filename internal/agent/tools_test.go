// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetToolByName(t *testing.T) {
	t.Parallel()

	tools := CreateTools(ToolConfig{})

	tests := []struct {
		name         string
		tools        []*AgentTool
		toolName     string
		expectNil    bool
		expectedName string
	}{
		{
			name:         "finds existing tool",
			tools:        tools,
			toolName:     "bash",
			expectNil:    false,
			expectedName: "bash",
		},
		{
			name:      "returns nil for unknown tool",
			tools:     tools,
			toolName:  "unknown",
			expectNil: true,
		},
		{
			name:      "returns nil for empty name",
			tools:     tools,
			toolName:  "",
			expectNil: true,
		},
		{
			name:      "returns nil for empty tools slice",
			tools:     []*AgentTool{},
			toolName:  "bash",
			expectNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tool := GetToolByName(tc.tools, tc.toolName)
			if tc.expectNil {
				assert.Nil(t, tool)
			} else {
				require.NotNil(t, tool)
				assert.Equal(t, tc.expectedName, tool.Function.Name)
			}
		})
	}
}

func TestDecodeToolInput(t *testing.T) {
	t.Parallel()

	t.Run("ignores unknown and malformed optional fields", func(t *testing.T) {
		t.Parallel()

		var got struct {
			Action string `json:"action"`
			Limit  int    `json:"limit,omitempty" lenient:"true"`
		}

		err := decodeToolInput(json.RawMessage(`{
			"action": "list",
			"limit": "not-an-integer",
			"extra": {"any": ["shape", 1, true]}
		}`), &got)

		require.NoError(t, err)
		assert.Equal(t, "list", got.Action)
		assert.Zero(t, got.Limit)
	})

	t.Run("rejects malformed known fields unless marked lenient", func(t *testing.T) {
		t.Parallel()

		var got struct {
			Path string `json:"path"`
		}

		err := decodeToolInput(json.RawMessage(`{"path": 123, "extra": "ignored"}`), &got)

		require.Error(t, err)
		assert.Contains(t, err.Error(), `field "path"`)
		assert.Empty(t, got.Path)
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		t.Parallel()

		var got struct {
			Action string `json:"action"`
		}

		err := decodeToolInput(json.RawMessage(`{"action":`), &got)

		require.Error(t, err)
	})
}

func TestToolError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		format   string
		args     []any
		expected string
	}{
		{
			name:     "creates error with formatted message",
			format:   "Error: %s",
			args:     []any{"test"},
			expected: "Error: test",
		},
		{
			name:     "creates error without format arguments",
			format:   "Simple error",
			args:     nil,
			expected: "Simple error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := toolError(tc.format, tc.args...)
			assert.True(t, result.IsError)
			assert.Equal(t, tc.expected, result.Content)
		})
	}
}

func TestResolvePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		workingDir string
		expected   string
	}{
		{
			name:       "absolute path unchanged",
			path:       "/abs/path/file.txt",
			workingDir: "/work",
			expected:   filepath.Clean("/abs/path/file.txt"),
		},
		{
			name:       "relative path joined with workingDir",
			path:       "rel/path/file.txt",
			workingDir: "/work",
			expected:   filepath.Join("/work", "rel/path/file.txt"),
		},
		{
			name:       "relative path with empty workingDir unchanged",
			path:       "rel/path/file.txt",
			workingDir: "",
			expected:   "rel/path/file.txt",
		},
		{
			name:       "simple filename joined",
			path:       "file.txt",
			workingDir: "/home/user",
			expected:   filepath.Join("/home/user", "file.txt"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := resolvePath(tc.path, tc.workingDir)
			assert.Equal(t, tc.expected, result)
		})
	}
}
