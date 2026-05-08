// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// Register executor capabilities for DAG validation tests.
	// In production, this is done by runtime/builtin init functions.
	for _, t := range []string{"", "shell", "command"} {
		core.RegisterExecutorCapabilities(t, core.ExecutorCapabilities{
			Command: true, MultipleCommands: true, Script: true, Shell: true,
		})
	}
	os.Exit(m.Run())
}

func patchInput(path, operation string, extra ...string) json.RawMessage {
	var base strings.Builder
	base.WriteString(fmt.Sprintf(`{"path": %q, "operation": %q`, path, operation))
	for i := 0; i < len(extra)-1; i += 2 {
		base.WriteString(fmt.Sprintf(`, %q: %q`, extra[i], extra[i+1]))
	}
	return json.RawMessage(base.String() + "}")
}

func skipIfWindowsFileMode(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permissions are not applicable on Windows")
	}
}

func TestPatchTool_Create(t *testing.T) {
	t.Parallel()
	tool := NewPatchTool("")

	t.Run("creates new file", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "new.txt")
		result := tool.Run(ToolContext{}, patchInput(filePath, "create", "content", "hello world"))

		assert.False(t, result.IsError)
		assert.Contains(t, result.Content, "Created")

		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "hello world", string(content))
	})

	t.Run("creates parent directories", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "nested", "deep", "file.txt")
		result := tool.Run(ToolContext{}, patchInput(filePath, "create", "content", "nested content"))

		assert.False(t, result.IsError)

		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "nested content", string(content))
	})

	t.Run("overwrites existing file", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "existing.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("old content"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "create", "content", "new content"))

		assert.False(t, result.IsError)

		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "new content", string(content))
	})

	t.Run("ignores unused operation-specific fields", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "new.txt")
		result := tool.Run(ToolContext{}, patchInput(
			filePath,
			"create",
			"content", "hello world",
			"old_string", "",
			"new_string", "",
			"anchor", "",
		))

		assert.False(t, result.IsError)
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "hello world", string(content))
	})

	t.Run("rejects malformed required content", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "new.txt")
		result := tool.Run(ToolContext{}, json.RawMessage(fmt.Sprintf(
			`{"path":%q,"operation":"create","content":{"wrong":"shape"},"old_string":"","anchor":""}`,
			filePath,
		)))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "content must be a string")
		_, err := os.Stat(filePath)
		assert.True(t, os.IsNotExist(err))
	})
}

func TestPatchTool_Replace(t *testing.T) {
	t.Parallel()
	tool := NewPatchTool("")

	t.Run("replaces unique string", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("hello world"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "replace", "old_string", "world", "new_string", "universe"))

		assert.False(t, result.IsError)
		assert.Contains(t, result.Content, "Replaced")

		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "hello universe", string(content))
	})

	t.Run("errors when old_string not found", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("hello world"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "replace", "old_string", "missing", "new_string", "replacement"))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "not found")
	})

	t.Run("errors when old_string found multiple times", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("hello hello hello"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "replace", "old_string", "hello", "new_string", "hi"))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "3 times")
	})

	t.Run("errors when file not found", func(t *testing.T) {
		t.Parallel()

		result := tool.Run(ToolContext{}, patchInput("/nonexistent/file.txt", "replace", "old_string", "a", "new_string", "b"))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "not found")
	})

	t.Run("errors when old_string is empty", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("content"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "replace", "old_string", "", "new_string", "b"))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "required")
	})

	t.Run("errors when old_string is whitespace", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("content"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "replace", "old_string", " \t\n", "new_string", "b"))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "old_string is required for replace operation")
	})

	t.Run("errors when new_string is missing", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("hello world"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "replace", "old_string", "world"))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "new_string is required")
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "hello world", string(content))
	})

	t.Run("allows explicit empty new_string", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("hello world"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "replace", "old_string", " world", "new_string", ""))

		assert.False(t, result.IsError, result.Content)
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(content))
	})

	t.Run("preserves existing file mode", func(t *testing.T) {
		t.Parallel()
		skipIfWindowsFileMode(t)

		filePath := filepath.Join(t.TempDir(), "mode.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("hello world"), 0o640))

		result := tool.Run(ToolContext{}, patchInput(filePath, "replace", "old_string", "world", "new_string", "universe"))

		assert.False(t, result.IsError, result.Content)
		info, err := os.Stat(filePath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	})

	t.Run("ignores irrelevant content field", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("hello world"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "replace", "old_string", "world", "new_string", "universe", "content", "extra"))

		assert.False(t, result.IsError)
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "hello universe", string(content))
	})

	t.Run("ignores irrelevant fields with null values", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("hello world"), 0o600))

		result := tool.Run(ToolContext{}, json.RawMessage(fmt.Sprintf(
			`{"path":%q,"operation":"replace","old_string":"world","new_string":"universe","content":null,"anchor":null}`,
			filePath,
		)))

		assert.False(t, result.IsError)
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "hello universe", string(content))
	})

	t.Run("ignores irrelevant fields with empty string values", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("hello world"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(
			filePath,
			"replace",
			"old_string", "world",
			"new_string", "universe",
			"content", "",
			"anchor", "",
		))

		assert.False(t, result.IsError)
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "hello universe", string(content))
	})

	t.Run("ignores malformed irrelevant fields", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("hello world"), 0o600))

		result := tool.Run(ToolContext{}, json.RawMessage(fmt.Sprintf(
			`{"path":%q,"operation":"replace","old_string":"world","new_string":"universe","content":{"unused":true},"anchor":["unused"]}`,
			filePath,
		)))

		assert.False(t, result.IsError)
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "hello universe", string(content))
	})
}

func TestPatchTool_Append(t *testing.T) {
	t.Parallel()
	tool := NewPatchTool("")

	t.Run("appends to end of file without deleting existing content", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "memory.md")
		require.NoError(t, os.WriteFile(filePath, []byte("- existing\n"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "append", "content", "- new\n"))

		assert.False(t, result.IsError, result.Content)
		assert.Contains(t, result.Content, "Appended")
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "- existing\n- new\n", string(content))
	})

	t.Run("adds newline before appending when file has no trailing newline", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "memory.md")
		require.NoError(t, os.WriteFile(filePath, []byte("- existing"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "append", "content", "- new\n"))

		assert.False(t, result.IsError, result.Content)
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "- existing\n- new\n", string(content))
	})

	t.Run("preserves existing file mode", func(t *testing.T) {
		t.Parallel()
		skipIfWindowsFileMode(t)

		filePath := filepath.Join(t.TempDir(), "mode.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("one\n"), 0o640))

		result := tool.Run(ToolContext{}, patchInput(filePath, "append", "content", "two\n"))

		assert.False(t, result.IsError, result.Content)
		info, err := os.Stat(filePath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	})

	t.Run("rejects empty append content without writing", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("one\n"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "append", "content", ""))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "content is required")
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "one\n", string(content))
	})
}

func TestPatchTool_Insert(t *testing.T) {
	t.Parallel()
	tool := NewPatchTool("")

	t.Run("insert_after keeps anchor and inserts content after it", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "memory.md")
		initial := "- YouTube動画の要約は `youtube_summary_jp` DAGを使用する\n- 日本語で要約を取得する\n"
		require.NoError(t, os.WriteFile(filePath, []byte(initial), 0o600))

		result := tool.Run(ToolContext{}, patchInput(
			filePath,
			"insert_after",
			"anchor", "- 日本語で要約を取得する\n",
			"content", "- ユーザーを「きみ」と呼ばない\n",
		))

		assert.False(t, result.IsError, result.Content)
		assert.Contains(t, result.Content, "Inserted")
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, initial+"- ユーザーを「きみ」と呼ばない\n", string(content))
	})

	t.Run("insert_before keeps anchor and inserts content before it", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "memory.md")
		initial := "- first\n- third\n"
		require.NoError(t, os.WriteFile(filePath, []byte(initial), 0o600))

		result := tool.Run(ToolContext{}, patchInput(
			filePath,
			"insert_before",
			"anchor", "- third\n",
			"content", "- second\n",
		))

		assert.False(t, result.IsError, result.Content)
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "- first\n- second\n- third\n", string(content))
	})

	t.Run("preserves existing file mode", func(t *testing.T) {
		t.Parallel()
		skipIfWindowsFileMode(t)

		filePath := filepath.Join(t.TempDir(), "mode.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("one\nthree\n"), 0o640))

		result := tool.Run(ToolContext{}, patchInput(
			filePath,
			"insert_after",
			"anchor", "one\n",
			"content", "two\n",
		))

		assert.False(t, result.IsError, result.Content)
		info, err := os.Stat(filePath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	})

	t.Run("rejects empty insert content without writing", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("one\n"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "insert_after", "anchor", "one\n", "content", ""))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "content is required")
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "one\n", string(content))
	})

	t.Run("ignores unused replace fields", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("one\n"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(
			filePath,
			"insert_after",
			"anchor", "one\n",
			"content", "two\n",
			"old_string", "",
			"new_string", "",
		))

		assert.False(t, result.IsError)
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "one\ntwo\n", string(content))
	})
}

func TestPatchTool_FailedEditsDoNotWrite(t *testing.T) {
	t.Parallel()
	tool := NewPatchTool("")

	tests := []struct {
		name     string
		initial  string
		input    json.RawMessage
		contains string
	}{
		{
			name:     "insert_after missing anchor",
			initial:  "alpha\n",
			input:    nil,
			contains: "anchor not found",
		},
		{
			name:     "insert_before duplicate anchor",
			initial:  "same\nsame\n",
			input:    nil,
			contains: "2 times",
		},
		{
			name:     "insert_after empty anchor",
			initial:  "alpha\n",
			input:    nil,
			contains: "anchor is required",
		},
		{
			name:     "replace missing old string",
			initial:  "alpha\n",
			input:    nil,
			contains: "not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			filePath := filepath.Join(t.TempDir(), "test.txt")
			require.NoError(t, os.WriteFile(filePath, []byte(tc.initial), 0o600))

			input := tc.input
			if input == nil {
				switch tc.name {
				case "insert_after missing anchor":
					input = patchInput(filePath, "insert_after", "anchor", "missing\n", "content", "new\n")
				case "insert_before duplicate anchor":
					input = patchInput(filePath, "insert_before", "anchor", "same\n", "content", "new\n")
				case "insert_after empty anchor":
					input = patchInput(filePath, "insert_after", "anchor", "", "content", "new\n")
				case "replace missing old string":
					input = patchInput(filePath, "replace", "old_string", "missing\n", "new_string", "new\n")
				}
			}

			result := tool.Run(ToolContext{}, input)

			assert.True(t, result.IsError)
			assert.Contains(t, result.Content, tc.contains)
			content, err := os.ReadFile(filePath)
			require.NoError(t, err)
			assert.Equal(t, tc.initial, string(content))
		})
	}

	t.Run("missing file returns error", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "missing.txt")
		result := tool.Run(ToolContext{}, patchInput(filePath, "append", "content", "new\n"))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "File not found")
	})

	t.Run("directory path returns error", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		result := tool.Run(ToolContext{}, patchInput(dir, "append", "content", "new\n"))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "is a directory")
	})
}

func TestPatchTool_Delete(t *testing.T) {
	t.Parallel()
	tool := NewPatchTool("")

	t.Run("deletes existing file", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "delete-me.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("content"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(filePath, "delete"))

		assert.False(t, result.IsError)
		assert.Contains(t, result.Content, "Deleted")

		_, err := os.Stat(filePath)
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("errors when file not found", func(t *testing.T) {
		t.Parallel()

		result := tool.Run(ToolContext{}, patchInput("/nonexistent/file.txt", "delete"))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "not found")
	})

	t.Run("ignores unused fields", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(t.TempDir(), "delete-me.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("content"), 0o600))

		result := tool.Run(ToolContext{}, patchInput(
			filePath,
			"delete",
			"content", "",
			"old_string", "",
			"new_string", "",
			"anchor", "",
		))

		assert.False(t, result.IsError)
		_, err := os.Stat(filePath)
		assert.True(t, os.IsNotExist(err))
	})
}

func TestPatchTool_Validation(t *testing.T) {
	t.Parallel()
	tool := NewPatchTool("")

	tests := []struct {
		name     string
		input    json.RawMessage
		contains string
	}{
		{
			name:     "empty path returns error",
			input:    patchInput("", "create", "content", "test"),
			contains: "required",
		},
		{
			name:     "unknown operation returns error",
			input:    patchInput("/test.txt", "unknown"),
			contains: "Unknown operation",
		},
		{
			name:     "invalid JSON returns error",
			input:    json.RawMessage(`{invalid}`),
			contains: "parse",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := tool.Run(ToolContext{}, tc.input)
			assert.True(t, result.IsError)
			assert.Contains(t, result.Content, tc.contains)
		})
	}
}

func TestPatchTool_WorkingDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	result := NewPatchTool("").Run(
		ToolContext{WorkingDir: dir},
		patchInput("test.txt", "create", "content", "content"),
	)

	assert.False(t, result.IsError)

	content, err := os.ReadFile(filepath.Join(dir, "test.txt"))
	require.NoError(t, err)
	assert.Equal(t, "content", string(content))
}

func TestPatchTool_Permissions(t *testing.T) {
	t.Parallel()

	tool := NewPatchTool("")
	filePath := filepath.Join(t.TempDir(), "test.txt")
	input := patchInput(filePath, "create", "content", "content")

	result := tool.Run(ToolContext{Role: auth.RoleOperator}, input)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "requires write permission")
}

func TestPatchTool_SchemaUsesProviderCompatibleObject(t *testing.T) {
	t.Parallel()

	params := NewPatchTool("").Function.Parameters
	assert.Equal(t, "object", params["type"])
	assert.NotContains(t, params, "oneOf")
	assert.NotContains(t, params, "anyOf")
	assert.NotContains(t, params, "allOf")
	assert.NotContains(t, params, "not")
	assert.NotContains(t, params, "additionalProperties")
	assert.Equal(t, []string{"path", "operation"}, params["required"])

	props, ok := params["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "path")
	assert.Contains(t, props, "operation")
	assert.Contains(t, props, "content")
	assert.Contains(t, props, "old_string")
	assert.Contains(t, props, "new_string")
	assert.Contains(t, props, "anchor")

	operation, ok := props["operation"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []string{"create", "replace", "append", "insert_before", "insert_after", "delete"}, operation["enum"])
}

func TestCountLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected int
	}{
		{"", 1},
		{"single line", 1},
		{"line1\nline2", 2},
		{"line1\nline2\nline3", 3},
		{"\n\n", 3},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, countLines(tc.input))
		})
	}
}

func TestIsDAGFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		dagsDir  string
		expected bool
	}{
		{
			name:     "yaml file in dags directory",
			path:     "/dags/workflow.yaml",
			dagsDir:  "/dags",
			expected: true,
		},
		{
			name:     "yaml file in subdirectory of dags",
			path:     "/dags/subdir/workflow.yaml",
			dagsDir:  "/dags",
			expected: true,
		},
		{
			name:     "non-yaml file in dags directory",
			path:     "/dags/readme.txt",
			dagsDir:  "/dags",
			expected: false,
		},
		{
			name:     "yaml file outside dags directory",
			path:     "/other/workflow.yaml",
			dagsDir:  "/dags",
			expected: false,
		},
		{
			name:     "empty dagsDir disables validation",
			path:     "/dags/workflow.yaml",
			dagsDir:  "",
			expected: false,
		},
		{
			name:     "yml extension not matched",
			path:     "/dags/workflow.yml",
			dagsDir:  "/dags",
			expected: false,
		},
		{
			name:     "path prefix bypass attempt",
			path:     "/dags-malicious/workflow.yaml",
			dagsDir:  "/dags",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, isDAGFile(tc.path, tc.dagsDir))
		})
	}
}

func TestValidateIfDAGFile(t *testing.T) {
	t.Parallel()

	t.Run("skips non-DAG files", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := filepath.Join(dir, "test.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("not a dag"), 0o600))

		errs := validateIfDAGFile(t.Context(), filePath, dir)
		assert.Empty(t, errs)
	})

	t.Run("skips when dagsDir is empty", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := filepath.Join(dir, "workflow.yaml")
		require.NoError(t, os.WriteFile(filePath, []byte("invalid: {{{"), 0o600))

		errs := validateIfDAGFile(t.Context(), filePath, "")
		assert.Empty(t, errs)
	})

	t.Run("returns no errors for valid DAG", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := filepath.Join(dir, "workflow.yaml")
		validDAG := `steps:
  - name: step1
    command: echo hello
`
		require.NoError(t, os.WriteFile(filePath, []byte(validDAG), 0o600))

		errs := validateIfDAGFile(t.Context(), filePath, dir)
		assert.Empty(t, errs)
	})

	t.Run("returns errors for invalid DAG", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := filepath.Join(dir, "workflow.yaml")
		invalidDAG := `steps:
  - name: step1
    command: echo hello
    timeout_sec: -1
`
		require.NoError(t, os.WriteFile(filePath, []byte(invalidDAG), 0o600))

		errs := validateIfDAGFile(t.Context(), filePath, dir)
		assert.NotEmpty(t, errs)
	})

	t.Run("returns error for malformed YAML", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := filepath.Join(dir, "workflow.yaml")
		require.NoError(t, os.WriteFile(filePath, []byte("invalid: {{{"), 0o600))

		errs := validateIfDAGFile(t.Context(), filePath, dir)
		assert.NotEmpty(t, errs)
	})
}

func TestPatchTool_DAGValidation(t *testing.T) {
	t.Parallel()

	t.Run("create shows validation errors for invalid DAG", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		tool := NewPatchTool(dir)
		filePath := filepath.Join(dir, "workflow.yaml")
		invalidDAG := `steps:
  - name: step1
    command: echo hello
    timeout_sec: -1
`
		result := tool.Run(ToolContext{}, patchInput(filePath, "create", "content", invalidDAG))

		assert.False(t, result.IsError)
		assert.Contains(t, result.Content, "Created")
		assert.Contains(t, result.Content, "DAG Validation Errors")
	})

	t.Run("create succeeds without errors for valid DAG", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		tool := NewPatchTool(dir)
		filePath := filepath.Join(dir, "workflow.yaml")
		validDAG := `steps:
  - name: step1
    command: echo hello
`
		result := tool.Run(ToolContext{}, patchInput(filePath, "create", "content", validDAG))

		assert.False(t, result.IsError)
		assert.Contains(t, result.Content, "Created")
		assert.NotContains(t, result.Content, "DAG Validation Errors")
	})

	t.Run("replace shows validation errors for invalid DAG", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		tool := NewPatchTool(dir)
		filePath := filepath.Join(dir, "workflow.yaml")
		initialDAG := `steps:
  - name: step1
    command: echo hello
    timeout_sec: 10
`
		require.NoError(t, os.WriteFile(filePath, []byte(initialDAG), 0o600))

		// Replace valid timeout with invalid negative timeout
		result := tool.Run(ToolContext{}, patchInput(filePath, "replace", "old_string", "timeout_sec: 10", "new_string", "timeout_sec: -1"))

		assert.False(t, result.IsError)
		assert.Contains(t, result.Content, "Replaced")
		assert.Contains(t, result.Content, "DAG Validation Errors")
	})

	t.Run("skips validation for non-yaml files", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		tool := NewPatchTool(dir)
		filePath := filepath.Join(dir, "script.sh")

		result := tool.Run(ToolContext{}, patchInput(filePath, "create", "content", "echo hello"))

		assert.False(t, result.IsError)
		assert.Contains(t, result.Content, "Created")
		assert.NotContains(t, result.Content, "DAG Validation Errors")
	})
}
