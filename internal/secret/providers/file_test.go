// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package providers

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileResolver_Name(t *testing.T) {
	registry := NewRegistry("/tmp")
	resolver := registry.Get("file")
	require.NotNil(t, resolver)
	assert.Equal(t, "file", resolver.Name())
}

func TestFileResolver_Validate(t *testing.T) {
	registry := NewRegistry("/tmp")
	resolver := registry.Get("file")
	require.NotNil(t, resolver)

	t.Run("ValidReference", func(t *testing.T) {
		ref := secretref.Ref{
			Name:     "API_KEY",
			Provider: "file",
			Key:      "/secrets/api_key",
		}
		err := resolver.Validate(ref)
		require.NoError(t, err)
	})

	t.Run("EmptyKey", func(t *testing.T) {
		ref := secretref.Ref{
			Name:     "SECRET",
			Provider: "file",
			Key:      "",
		}
		err := resolver.Validate(ref)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "key")
		assert.Contains(t, err.Error(), "required")
	})
}

func TestFileResolver_Resolve(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	registry := NewRegistry(tmpDir)
	resolver := registry.Get("file")
	require.NotNil(t, resolver)

	t.Run("ReadFileAbsolutePath", func(t *testing.T) {
		secretFile := filepath.Join(tmpDir, "secret.txt")
		secretValue := "my_secret_value"
		require.NoError(t, os.WriteFile(secretFile, []byte(secretValue), 0600))

		ref := secretref.Ref{
			Name:     "API_KEY",
			Provider: "file",
			Key:      secretFile,
		}

		value, err := resolver.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, secretValue, value)
	})

	t.Run("ReadFileRelativePath", func(t *testing.T) {
		secretValue := "relative_secret"
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "relative.txt"), []byte(secretValue), 0600))

		ref := secretref.Ref{
			Name:     "RELATIVE_SECRET",
			Provider: "file",
			Key:      "relative.txt",
		}

		value, err := resolver.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, secretValue, value)
	})

	t.Run("FileNotFound", func(t *testing.T) {
		ref := secretref.Ref{
			Name:     "MISSING",
			Provider: "file",
			Key:      "/nonexistent/path/secret.txt",
		}

		_, err := resolver.Resolve(ctx, ref)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("FileInSubdirectory", func(t *testing.T) {
		subDir := filepath.Join(tmpDir, "secrets", "production")
		require.NoError(t, os.MkdirAll(subDir, 0700))

		secretFile := filepath.Join(subDir, "api_key.txt")
		secretValue := "production_secret"
		require.NoError(t, os.WriteFile(secretFile, []byte(secretValue), 0600))

		ref := secretref.Ref{
			Name:     "PROD_API_KEY",
			Provider: "file",
			Key:      secretFile,
		}

		value, err := resolver.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, secretValue, value)
	})

	t.Run("EmptyFile", func(t *testing.T) {
		emptyFile := filepath.Join(tmpDir, "empty.txt")
		require.NoError(t, os.WriteFile(emptyFile, []byte(""), 0600))

		ref := secretref.Ref{
			Name:     "EMPTY",
			Provider: "file",
			Key:      emptyFile,
		}

		value, err := resolver.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, "", value)
	})

	t.Run("FileWithWhitespace", func(t *testing.T) {
		whitespaceFile := filepath.Join(tmpDir, "whitespace.txt")
		content := "  secret with spaces  \n"
		require.NoError(t, os.WriteFile(whitespaceFile, []byte(content), 0600))

		ref := secretref.Ref{
			Name:     "WHITESPACE_SECRET",
			Provider: "file",
			Key:      whitespaceFile,
		}

		value, err := resolver.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, content, value, "should preserve exact file content")
	})

	t.Run("MultilineFile", func(t *testing.T) {
		multilineFile := filepath.Join(tmpDir, "multiline.txt")
		content := "line1\nline2\nline3\n"
		require.NoError(t, os.WriteFile(multilineFile, []byte(content), 0600))

		ref := secretref.Ref{
			Name:     "MULTILINE",
			Provider: "file",
			Key:      multilineFile,
		}

		value, err := resolver.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, content, value)
	})

	t.Run("BinaryFile", func(t *testing.T) {
		binaryFile := filepath.Join(tmpDir, "binary.dat")
		binaryData := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE}
		require.NoError(t, os.WriteFile(binaryFile, binaryData, 0600))

		ref := secretref.Ref{
			Name:     "BINARY",
			Provider: "file",
			Key:      binaryFile,
		}

		value, err := resolver.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, string(binaryData), value)
	})

	t.Run("LargeFile", func(t *testing.T) {
		largeFile := filepath.Join(tmpDir, "large.txt")
		largeContent := string(make([]byte, 1024*1024)) // 1MB
		require.NoError(t, os.WriteFile(largeFile, []byte(largeContent), 0600))

		ref := secretref.Ref{
			Name:     "LARGE",
			Provider: "file",
			Key:      largeFile,
		}

		value, err := resolver.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Len(t, value, len(largeContent))
	})
}

func TestFileResolver_Resolve_PermissionErrors(t *testing.T) {
	// Skip on Windows - file permissions work differently
	if runtime.GOOS == "windows" {
		t.Skip("Skipping permission test on Windows")
	}

	ctx := context.Background()
	tmpDir := t.TempDir()
	registry := NewRegistry(tmpDir)
	resolver := registry.Get("file")
	require.NotNil(t, resolver)

	t.Run("UnreadableFile", func(t *testing.T) {
		unreadableFile := filepath.Join(tmpDir, "unreadable.txt")
		require.NoError(t, os.WriteFile(unreadableFile, []byte("secret"), 0000))

		ref := secretref.Ref{
			Name:     "UNREADABLE",
			Provider: "file",
			Key:      unreadableFile,
		}

		_, err := resolver.Resolve(ctx, ref)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "permission denied")
	})
}

func TestFileResolver_CheckAccessibility(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	registry := NewRegistry(tmpDir)
	resolver := registry.Get("file")
	require.NotNil(t, resolver)

	t.Run("AccessibleFile", func(t *testing.T) {
		accessibleFile := filepath.Join(tmpDir, "accessible.txt")
		require.NoError(t, os.WriteFile(accessibleFile, []byte("value"), 0600))

		ref := secretref.Ref{
			Name:     "ACCESSIBLE",
			Provider: "file",
			Key:      accessibleFile,
		}

		err := resolver.CheckAccessibility(ctx, ref)
		require.NoError(t, err)
	})

	t.Run("FileNotFound", func(t *testing.T) {
		ref := secretref.Ref{
			Name:     "MISSING",
			Provider: "file",
			Key:      "/nonexistent/file.txt",
		}

		err := resolver.CheckAccessibility(ctx, ref)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("DirectoryInsteadOfFile", func(t *testing.T) {
		dirPath := filepath.Join(tmpDir, "directory")
		require.NoError(t, os.Mkdir(dirPath, 0700))

		ref := secretref.Ref{
			Name:     "DIR",
			Provider: "file",
			Key:      dirPath,
		}

		err := resolver.CheckAccessibility(ctx, ref)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "directory")
	})

	t.Run("RelativePathAccessibility", func(t *testing.T) {
		relativeFile := "relative_accessible.txt"
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, relativeFile), []byte("value"), 0600))

		ref := secretref.Ref{
			Name:     "RELATIVE",
			Provider: "file",
			Key:      relativeFile,
		}

		err := resolver.CheckAccessibility(ctx, ref)
		require.NoError(t, err)
	})

	t.Run("EmptyFileIsAccessible", func(t *testing.T) {
		emptyFile := filepath.Join(tmpDir, "empty_accessible.txt")
		require.NoError(t, os.WriteFile(emptyFile, []byte(""), 0600))

		ref := secretref.Ref{
			Name:     "EMPTY",
			Provider: "file",
			Key:      emptyFile,
		}

		err := resolver.CheckAccessibility(ctx, ref)
		require.NoError(t, err)
	})
}

func TestFileResolver_CheckAccessibility_PermissionErrors(t *testing.T) {
	// Skip on Windows - file permissions work differently
	if runtime.GOOS == "windows" {
		t.Skip("Skipping permission test on Windows")
	}

	ctx := context.Background()
	tmpDir := t.TempDir()
	registry := NewRegistry(tmpDir)
	resolver := registry.Get("file")
	require.NotNil(t, resolver)

	t.Run("UnreadableFile", func(t *testing.T) {
		unreadableFile := filepath.Join(tmpDir, "unreadable_check.txt")
		require.NoError(t, os.WriteFile(unreadableFile, []byte("secret"), 0000))

		ref := secretref.Ref{
			Name:     "UNREADABLE",
			Provider: "file",
			Key:      unreadableFile,
		}

		err := resolver.CheckAccessibility(ctx, ref)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not readable")
	})
}

func TestFileResolver_PathResolution(t *testing.T) {
	ctx := context.Background()

	t.Run("AbsolutePathIgnoresWorkingDir", func(t *testing.T) {
		tmpDir := t.TempDir()
		registry := NewRegistry("/wrong/directory")
		resolver := registry.Get("file")
		require.NotNil(t, resolver)

		absoluteFile := filepath.Join(tmpDir, "absolute.txt")
		secretValue := "absolute_value"
		require.NoError(t, os.WriteFile(absoluteFile, []byte(secretValue), 0600))

		ref := secretref.Ref{
			Name:     "ABSOLUTE",
			Provider: "file",
			Key:      absoluteFile,
		}

		value, err := resolver.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, secretValue, value)
	})

	t.Run("RelativePathUsesWorkingDir", func(t *testing.T) {
		tmpDir := t.TempDir()
		registry := NewRegistry(tmpDir)
		resolver := registry.Get("file")
		require.NotNil(t, resolver)

		secretValue := "relative_value"
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "relative.txt"), []byte(secretValue), 0600))

		ref := secretref.Ref{
			Name:     "RELATIVE",
			Provider: "file",
			Key:      "relative.txt",
		}

		value, err := resolver.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, secretValue, value)
	})

	t.Run("EmptyWorkingDirWithRelativePath", func(t *testing.T) {
		registry := NewRegistry("")
		resolver := registry.Get("file")
		require.NotNil(t, resolver)

		ref := secretref.Ref{
			Name:     "RELATIVE",
			Provider: "file",
			Key:      "nonexistent.txt",
		}

		_, err := resolver.Resolve(ctx, ref)
		require.Error(t, err, "should fail without proper working directory")
	})

	t.Run("SubdirectoryRelativePath", func(t *testing.T) {
		tmpDir := t.TempDir()
		registry := NewRegistry(tmpDir)
		resolver := registry.Get("file")
		require.NotNil(t, resolver)

		subDir := filepath.Join(tmpDir, "secrets")
		require.NoError(t, os.Mkdir(subDir, 0700))

		secretFile := filepath.Join(subDir, "key.txt")
		secretValue := "subdir_secret"
		require.NoError(t, os.WriteFile(secretFile, []byte(secretValue), 0600))

		ref := secretref.Ref{
			Name:     "SUBDIR",
			Provider: "file",
			Key:      "secrets/key.txt",
		}

		value, err := resolver.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, secretValue, value)
	})
}

func TestFileResolver_OptionsHandling(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	registry := NewRegistry(tmpDir)
	resolver := registry.Get("file")
	require.NotNil(t, resolver)

	t.Run("UnknownOptionsIgnored", func(t *testing.T) {
		secretFile := filepath.Join(tmpDir, "options.txt")
		secretValue := "value"
		require.NoError(t, os.WriteFile(secretFile, []byte(secretValue), 0600))

		ref := secretref.Ref{
			Name:     "SECRET",
			Provider: "file",
			Key:      secretFile,
			Options: map[string]string{
				"unknown": "option",
				"custom":  "value",
			},
		}

		value, err := resolver.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, secretValue, value)
	})

	t.Run("NilOptionsAllowed", func(t *testing.T) {
		secretFile := filepath.Join(tmpDir, "nil_options.txt")
		secretValue := "value"
		require.NoError(t, os.WriteFile(secretFile, []byte(secretValue), 0600))

		ref := secretref.Ref{
			Name:     "SECRET",
			Provider: "file",
			Key:      secretFile,
			Options:  nil,
		}

		value, err := resolver.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, secretValue, value)
	})
}

func TestFileResolver_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	registry := NewRegistry(tmpDir)
	resolver := registry.Get("file")
	require.NotNil(t, resolver)

	secretFile := filepath.Join(tmpDir, "concurrent.txt")
	secretValue := "concurrent_value"
	require.NoError(t, os.WriteFile(secretFile, []byte(secretValue), 0600))

	ref := secretref.Ref{
		Name:     "CONCURRENT",
		Provider: "file",
		Key:      secretFile,
	}

	// Run multiple goroutines concurrently
	const numGoroutines = 100
	done := make(chan bool, numGoroutines)
	errors := make(chan error, numGoroutines)

	for range numGoroutines {
		go func() {
			value, err := resolver.Resolve(ctx, ref)
			if err != nil {
				errors <- err
			} else if value != secretValue {
				errors <- assert.AnError
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for range numGoroutines {
		<-done
	}

	close(errors)
	for err := range errors {
		t.Errorf("Concurrent access error: %v", err)
	}
}
