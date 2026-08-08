// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func TestNewContextStoreRejectsNilConfig(t *testing.T) {
	t.Parallel()

	store, err := file.NewContextStore(nil)

	require.Error(t, err)
	assert.Nil(t, store)
	assert.Contains(t, err.Error(), "config cannot be nil")
}

func TestNewEventCollectorDisabledWhenConfigNilOrEventStoreDisabled(t *testing.T) {
	t.Parallel()

	collector, err := file.NewEventCollector(nil)
	require.NoError(t, err)
	assert.Nil(t, collector)

	collector, err = file.NewEventCollector(&config.Config{})
	require.NoError(t, err)
	assert.Nil(t, collector)
}

func TestNewDocStoreCreatesDocumentDirectory(t *testing.T) {
	t.Parallel()

	docsDir := filepath.Join(t.TempDir(), "nested", "docs")
	store, err := file.NewDocStore(&config.Config{Paths: config.PathsConfig{DocsDir: docsDir}})

	require.NoError(t, err)
	assert.NotNil(t, store)
	info, statErr := os.Stat(docsDir)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())
}

func TestNewDocStoreReturnsDirectoryCreationError(t *testing.T) {
	t.Parallel()

	blockingFile := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blockingFile, []byte("content"), 0o600))
	store, err := file.NewDocStore(&config.Config{
		Paths: config.PathsConfig{DocsDir: filepath.Join(blockingFile, "docs")},
	})

	require.Error(t, err)
	assert.Nil(t, store)
}

func TestTokenSecretProvider(t *testing.T) {
	t.Run("auto-generates when file missing", func(t *testing.T) {
		dataDir := t.TempDir()
		authDir := filepath.Join(dataDir, "auth")

		provider := file.NewTokenSecretProvider(tokenSecretProviderConfig(dataDir))
		secret, err := provider.Resolve(context.Background())
		require.NoError(t, err)
		assert.True(t, secret.IsValid())

		path := filepath.Join(authDir, "token_secret")
		info, err := os.Stat(path)
		require.NoError(t, err)
		if testutil.SupportsPOSIXPermissionBits() {
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		}

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Len(t, string(data), 43)
	})

	t.Run("reads existing file", func(t *testing.T) {
		dataDir := t.TempDir()
		authDir := filepath.Join(dataDir, "auth")
		require.NoError(t, os.MkdirAll(authDir, 0o700))
		path := filepath.Join(authDir, "token_secret")
		require.NoError(t, os.WriteFile(path, []byte("existing-secret"), 0o600))

		provider := file.NewTokenSecretProvider(tokenSecretProviderConfig(dataDir))
		secret, err := provider.Resolve(context.Background())
		require.NoError(t, err)
		assert.Equal(t, []byte("existing-secret"), secret.SigningKey())
	})

	t.Run("regenerates on empty file", func(t *testing.T) {
		dataDir := t.TempDir()
		authDir := filepath.Join(dataDir, "auth")
		require.NoError(t, os.MkdirAll(authDir, 0o700))
		path := filepath.Join(authDir, "token_secret")
		require.NoError(t, os.WriteFile(path, nil, 0o600))

		provider := file.NewTokenSecretProvider(tokenSecretProviderConfig(dataDir))
		secret, err := provider.Resolve(context.Background())
		require.NoError(t, err)
		assert.True(t, secret.IsValid())

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Len(t, string(data), 43)
	})

	t.Run("regenerates on whitespace-only file", func(t *testing.T) {
		dataDir := t.TempDir()
		authDir := filepath.Join(dataDir, "auth")
		require.NoError(t, os.MkdirAll(authDir, 0o700))
		path := filepath.Join(authDir, "token_secret")
		require.NoError(t, os.WriteFile(path, []byte("  \n\t  "), 0o600))

		provider := file.NewTokenSecretProvider(tokenSecretProviderConfig(dataDir))
		secret, err := provider.Resolve(context.Background())
		require.NoError(t, err)
		assert.True(t, secret.IsValid())
	})

	t.Run("stable across calls", func(t *testing.T) {
		dataDir := t.TempDir()
		provider := file.NewTokenSecretProvider(tokenSecretProviderConfig(dataDir))

		first, err := provider.Resolve(context.Background())
		require.NoError(t, err)
		second, err := provider.Resolve(context.Background())
		require.NoError(t, err)

		assert.Equal(t, first.SigningKey(), second.SigningKey())
	})

	t.Run("directory permissions", func(t *testing.T) {
		dataDir := t.TempDir()
		authDir := filepath.Join(dataDir, "auth")

		provider := file.NewTokenSecretProvider(tokenSecretProviderConfig(dataDir))
		_, err := provider.Resolve(context.Background())
		require.NoError(t, err)

		info, err := os.Stat(authDir)
		require.NoError(t, err)
		if testutil.SupportsPOSIXPermissionBits() {
			assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
		}
	})

	t.Run("concurrent resolve converges to same secret", func(t *testing.T) {
		dataDir := t.TempDir()
		cfg := tokenSecretProviderConfig(dataDir)

		const goroutines = 10
		secrets := make([][]byte, goroutines)
		errs := make([]error, goroutines)

		var waitGroup sync.WaitGroup
		waitGroup.Add(goroutines)
		for i := range goroutines {
			go func(index int) {
				defer waitGroup.Done()
				secret, err := file.NewTokenSecretProvider(cfg).Resolve(context.Background())
				errs[index] = err
				if err == nil {
					secrets[index] = secret.SigningKey()
				}
			}(i)
		}
		waitGroup.Wait()

		for i, err := range errs {
			require.NoError(t, err, "goroutine %d", i)
		}
		for i := 1; i < goroutines; i++ {
			assert.Equal(t, secrets[0], secrets[i], "goroutine %d has different secret", i)
		}
	})
}

func tokenSecretProviderConfig(dataDir string) *config.Config {
	return &config.Config{Paths: config.PathsConfig{DataDir: dataDir}}
}
