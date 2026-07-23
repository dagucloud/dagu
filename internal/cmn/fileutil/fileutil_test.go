// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateFile(t *testing.T) {
	t.Parallel()

	t.Run("FileCreationAndPermissions", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := filepath.Join(dir, "test.log")

		file, err := OpenOrCreateFile(filePath)
		require.NoError(t, err)
		defer func() {
			_ = file.Close()
		}()

		assert.NotNil(t, file)
		assert.Equal(t, filePath, file.Name())

		info, err := file.Stat()
		require.NoError(t, err)
		expectedPerm := os.FileMode(0600)
		if runtime.GOOS == "windows" {
			expectedPerm = 0666
		}
		assert.Equal(t, expectedPerm, info.Mode().Perm())
	})

	t.Run("InvalidPath", func(t *testing.T) {
		t.Parallel()

		// Create a temp directory and remove it to get a guaranteed invalid path
		dir := t.TempDir()
		invalidPath := filepath.Join(dir, "removed", "test.log")

		_, err := OpenOrCreateFile(invalidPath)
		assert.Error(t, err)
	})
}

func TestOpenOrCreateFileWithoutSync(t *testing.T) {
	t.Parallel()

	filePath := filepath.Join(t.TempDir(), "stream.log")
	file, err := OpenOrCreateFileWithoutSync(filePath)
	require.NoError(t, err)
	defer func() {
		_ = file.Close()
	}()

	_, err = file.WriteString("first")
	require.NoError(t, err)

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, "first", string(data))

	second, err := OpenOrCreateFileWithoutSync(filePath)
	require.NoError(t, err)
	defer func() {
		_ = second.Close()
	}()

	_, err = second.WriteString("-second")
	require.NoError(t, err)

	data, err = os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, "first-second", string(data))
}

func TestResolvePath(t *testing.T) {
	// Get current working directory for absolute path tests
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current working directory: %v", err)
	}

	tmpRoot := t.TempDir()
	testHome := filepath.Join(tmpRoot, "home")
	testTempDir := filepath.Join("temp", "dir")

	t.Setenv("HOME", testHome)
	t.Setenv("TEMP_DIR", testTempDir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", testHome)
		volume := filepath.VolumeName(testHome)
		if volume != "" {
			t.Setenv("HOMEDRIVE", volume)
			t.Setenv("HOMEPATH", strings.TrimPrefix(testHome, volume))
		}
	}

	absolutePath, err := filepath.Abs("/usr/local/bin")
	require.NoError(t, err)
	pathWithDots, err := filepath.Abs("/usr/local/../bin/./app")
	require.NoError(t, err)
	redundantSlashesPath, err := filepath.Abs("/usr//local/bin")
	require.NoError(t, err)

	tests := []struct {
		name        string
		path        string
		expected    string
		expectError bool
	}{
		{
			name:        "EmptyPath",
			path:        "",
			expected:    "",
			expectError: false,
		},
		{
			name:        "TildeExpansion",
			path:        "~/documents",
			expected:    filepath.Clean(filepath.Join(testHome, "documents")),
			expectError: false,
		},
		{
			name:        "TildeOnly",
			path:        "~",
			expected:    filepath.Clean(testHome),
			expectError: false,
		},
		{
			name:        "EnvironmentVariableExpansion",
			path:        "$TEMP_DIR/logs",
			expected:    filepath.Clean(filepath.Join(cwd, testTempDir, "logs")),
			expectError: false,
		},
		{
			name:        "MultipleEnvironmentVariables",
			path:        "$HOME/projects/$TEMP_DIR",
			expected:    filepath.Clean(filepath.Join(testHome, "projects", testTempDir)),
			expectError: false,
		},
		{
			name:        "PathCleaningWithDots",
			path:        "/usr/local/../bin/./app",
			expected:    filepath.Clean(pathWithDots),
			expectError: false,
		},
		{
			name:        "PathCleaningWithRedundantSlashes",
			path:        "/usr//local/bin",
			expected:    filepath.Clean(redundantSlashesPath),
			expectError: false,
		},
		{
			name:        "CombinedTildeAndEnvironmentVariable",
			path:        "~/projects/$TEMP_DIR",
			expected:    filepath.Clean(filepath.Join(testHome, "projects", testTempDir)),
			expectError: false,
		},
		{
			name:        "AbsolutePath",
			path:        "/usr/local/bin",
			expected:    filepath.Clean(absolutePath),
			expectError: false,
		},
		{
			name:        "RelativePath",
			path:        "projects/dagu",
			expected:    filepath.Join(cwd, "projects/dagu"),
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolvePath(tt.path)

			// Check error expectation
			if (err != nil) != tt.expectError {
				t.Errorf("ResolvePath(%q) error = %v, expectError %v", tt.path, err, tt.expectError)
				return
			}

			if tt.expectError {
				return // No need to check the result if we expected an error
			}

			// For empty path, we expect empty result
			if tt.path == "" {
				if result != "" {
					t.Errorf("ResolvePath(%q) = %q, want %q", tt.path, result, "")
				}
				return
			}

			// For all other paths, check the result matches expected
			if result != tt.expected {
				t.Errorf("ResolvePath(%q) = %q, want %q", tt.path, result, tt.expected)
			}
		})
	}
}

func TestMustResolvePath(t *testing.T) {
	// Test normal case
	t.Run("NormalCase", func(t *testing.T) {
		// Get current working directory
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Failed to get current working directory: %v", err)
		}

		path := "test.txt"
		expected := filepath.Join(cwd, path)

		result := ResolvePathOrBlank(path)
		if result != expected {
			t.Errorf("MustResolvePath(%q) = %q, want %q", path, result, expected)
		}
	})

	// Test panic case - can't easily test without mocking os functions
	// but we can at least verify it calls ResolvePath
	t.Run("CallsResolvePath", func(t *testing.T) {
		path := "test.txt"
		resolved, err := ResolvePath(path)
		if err != nil {
			t.Fatalf("ResolvePath failed: %v", err)
		}

		mustResolved := ResolvePathOrBlank(path)
		if mustResolved != resolved {
			t.Errorf("MustResolvePath(%q) = %q, but ResolvePath(%q) = %q",
				path, mustResolved, path, resolved)
		}
	})
}

func TestIsFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	t.Run("RegularFile", func(t *testing.T) {
		t.Parallel()

		filePath := filepath.Join(tmpDir, "testfile.txt")
		err := os.WriteFile(filePath, []byte("test content"), 0644)
		require.NoError(t, err)

		require.True(t, IsFile(filePath))
	})

	t.Run("Directory", func(t *testing.T) {
		t.Parallel()

		dirPath := filepath.Join(tmpDir, "testdir")
		err := os.Mkdir(dirPath, 0755)
		require.NoError(t, err)

		require.False(t, IsFile(dirPath))
	})

	t.Run("NonExistent", func(t *testing.T) {
		t.Parallel()

		require.False(t, IsFile(filepath.Join(tmpDir, "nonexistent")))
	})
}

func TestCreateTempDAGFile(t *testing.T) {
	t.Parallel()

	t.Run("BasicFile", func(t *testing.T) {
		t.Parallel()
		yamlData := []byte("name: test-dag\nsteps:\n  - name: step1\n    command: echo test")
		path, err := CreateTempDAGFile("test-subdir", "test-dag", yamlData)
		require.NoError(t, err)
		require.NotEmpty(t, path)
		t.Cleanup(func() { _ = os.Remove(path) })

		// Verify file exists
		assert.FileExists(t, path)

		// Verify content
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, yamlData, content)

		// Verify path contains expected components
		assert.Contains(t, path, "test-dag")
		assert.Contains(t, path, ".yaml")
		assert.Contains(t, path, filepath.Join("dagu", "test-subdir"))
	})

	t.Run("WithExtraDocs", func(t *testing.T) {
		t.Parallel()

		primaryDoc := []byte("name: parent-dag\nsteps:\n  - name: step1")
		extraDoc1 := []byte("name: child1\nsteps:\n  - name: s1")
		extraDoc2 := []byte("name: child2\nsteps:\n  - name: s2")

		path, err := CreateTempDAGFile("test-subdir", "parent-dag", primaryDoc, extraDoc1, extraDoc2)
		require.NoError(t, err)
		require.NotEmpty(t, path)
		t.Cleanup(func() { _ = os.Remove(path) })

		// Verify file exists
		assert.FileExists(t, path)

		// Verify content has all docs separated by ---
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		contentStr := string(content)

		assert.Contains(t, contentStr, "name: parent-dag")
		assert.Contains(t, contentStr, "name: child1")
		assert.Contains(t, contentStr, "name: child2")

		// Verify YAML separators appear on their own line (not appended to previous content)
		// The separator should be "\n---\n" format per YAML spec
		assert.Contains(t, contentStr, "\n---\n", "YAML separator should appear on its own line")
		assert.NotContains(t, contentStr, "step1---", "separator should not be appended directly to content")
	})

	t.Run("WithExtraDocs_NoTrailingNewline", func(t *testing.T) {
		t.Parallel()

		// Primary doc without trailing newline - should still produce valid YAML
		primaryDoc := []byte("name: parent-dag")
		extraDoc := []byte("name: child-dag")

		path, err := CreateTempDAGFile("test-subdir", "no-newline-dag", primaryDoc, extraDoc)
		require.NoError(t, err)
		require.NotEmpty(t, path)
		t.Cleanup(func() { _ = os.Remove(path) })

		content, err := os.ReadFile(path)
		require.NoError(t, err)
		contentStr := string(content)

		// Should have newline inserted before separator
		assert.Contains(t, contentStr, "name: parent-dag\n---\n", "newline should be inserted before separator when missing")
	})

	t.Run("LongDAGName_BasenameFitsLimit", func(t *testing.T) {
		t.Parallel()

		// A 40-char DAG name is at the max allowed length. The temp file
		// basename (without .yaml) must still be ≤ 40 chars so that
		// buildName / ValidateDAGName won't reject it.
		longName := "aaaaaaaaaa_bbbbbbbbbbb_ccccccccccccccccc" // exactly 40 chars
		require.Len(t, longName, 40)

		yamlData := []byte("steps:\n  - name: step1\n    command: echo ok")
		path, err := CreateTempDAGFile("test-subdir", longName, yamlData)
		require.NoError(t, err)
		require.NotEmpty(t, path)
		t.Cleanup(func() { _ = os.Remove(path) })

		base := filepath.Base(path)
		nameWithoutExt := strings.TrimSuffix(base, filepath.Ext(base))
		assert.LessOrEqual(t, len(nameWithoutExt), 40,
			"basename without extension should be ≤ 40 chars, got %d: %s", len(nameWithoutExt), nameWithoutExt)
	})

	t.Run("PathLikeNameUsesBasename", func(t *testing.T) {
		t.Parallel()

		dagPath := filepath.Join(t.TempDir(), "nested", "worker-retry.yaml")
		path, err := CreateTempDAGFile("test-subdir", dagPath, []byte("steps:\n  - name: step1\n"))
		require.NoError(t, err)
		require.NotEmpty(t, path)
		t.Cleanup(func() { _ = os.Remove(path) })

		base := filepath.Base(path)
		assert.Contains(t, base, "worker-retry")
		assert.NotContains(t, base, string(os.PathSeparator))
		assert.NotContains(t, base, ".yaml-")
	})

	t.Run("WhitespaceOnlyNameUsesFallback", func(t *testing.T) {
		t.Parallel()

		path, err := CreateTempDAGFile("test-subdir", "   ", []byte("steps:\n  - name: step1\n"))
		require.NoError(t, err)
		require.NotEmpty(t, path)
		t.Cleanup(func() { _ = os.Remove(path) })

		base := filepath.Base(path)
		assert.Contains(t, base, "dag-")
		assert.NotContains(t, base, "   ")
	})

	t.Run("EmptyExtraDocs", func(t *testing.T) {
		t.Parallel()

		primaryDoc := []byte("name: solo-dag")

		path, err := CreateTempDAGFile("test-subdir", "solo-dag", primaryDoc)
		require.NoError(t, err)
		require.NotEmpty(t, path)
		t.Cleanup(func() { _ = os.Remove(path) })

		// Verify content has only the primary doc (no separators)
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, primaryDoc, content)
		assert.NotContains(t, string(content), "---")
	})
}
