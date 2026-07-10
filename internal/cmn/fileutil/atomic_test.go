// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteFileAtomic(t *testing.T) {
	t.Parallel()

	t.Run("writes file successfully", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		filePath := filepath.Join(dir, "test.txt")
		content := []byte("hello world")

		err := WriteFileAtomic(filePath, content, 0600)
		require.NoError(t, err)

		data, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, content, data)

		// Verify temp file was cleaned up
		_, err = os.Stat(filePath + ".tmp")
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("overwrites existing file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		filePath := filepath.Join(dir, "test.txt")

		require.NoError(t, os.WriteFile(filePath, []byte("old"), 0600))

		err := WriteFileAtomic(filePath, []byte("new"), 0600)
		require.NoError(t, err)

		data, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, []byte("new"), data)
	})

	t.Run("returns error for invalid path", func(t *testing.T) {
		t.Parallel()
		err := WriteFileAtomic("/nonexistent/dir/file.txt", []byte("data"), 0600)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create temp file")
	})

	t.Run("sets correct permissions", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("Unix file permissions not applicable on Windows")
		}
		dir := t.TempDir()
		filePath := filepath.Join(dir, "test.txt")

		err := WriteFileAtomic(filePath, []byte("data"), 0644)
		require.NoError(t, err)

		info, err := os.Stat(filePath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
	})
}

func TestWriteFileAtomicExclusive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "exclusive.txt")
	require.NoError(t, WriteFileAtomicExclusive(filePath, []byte("first"), 0o600))

	err := WriteFileAtomicExclusive(filePath, []byte("second"), 0o600)
	assert.ErrorIs(t, err, fs.ErrExist)

	data, readErr := os.ReadFile(filePath)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("first"), data)

	temps, globErr := filepath.Glob(filePath + ".tmp.*")
	require.NoError(t, globErr)
	assert.Empty(t, temps)
}

func TestWriteJSONAtomic(t *testing.T) {
	t.Parallel()

	type testData struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	t.Run("writes JSON successfully", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		filePath := filepath.Join(dir, "test.json")
		data := testData{Name: "test", Value: 42}

		err := WriteJSONAtomic(filePath, data, 0600)
		require.NoError(t, err)

		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		expected := "{\n  \"name\": \"test\",\n  \"value\": 42\n}"
		assert.Equal(t, expected, string(content))
	})

	t.Run("writes nil as null", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		filePath := filepath.Join(dir, "test.json")

		err := WriteJSONAtomic(filePath, nil, 0600)
		require.NoError(t, err)

		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "null", string(content))
	})

	t.Run("returns error for unmarshalable type", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		filePath := filepath.Join(dir, "test.json")

		err := WriteJSONAtomic(filePath, make(chan int), 0600)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to marshal JSON")
	})

	t.Run("returns error for invalid path", func(t *testing.T) {
		t.Parallel()
		err := WriteJSONAtomic("/nonexistent/dir/file.json", "data", 0600)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create temp file")
	})
}

func TestReplaceFileWithRetryReplacesExistingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source.tmp")
	target := filepath.Join(dir, "target.txt")

	require.NoError(t, os.WriteFile(source, []byte("new"), 0600))
	require.NoError(t, os.WriteFile(target, []byte("old"), 0600))

	require.NoError(t, ReplaceFileWithRetry(source, target))

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, []byte("new"), data)

	_, err = os.Stat(source)
	assert.True(t, os.IsNotExist(err))
}
