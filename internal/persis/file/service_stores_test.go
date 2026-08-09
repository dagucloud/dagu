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

func TestNewWikiStoreCreatesWikiDirectory(t *testing.T) {
	t.Parallel()

	wikiDir := filepath.Join(t.TempDir(), "nested", "wiki")
	store, err := file.NewWikiStore(&config.Config{Paths: config.PathsConfig{WikiDir: wikiDir}})

	require.NoError(t, err)
	assert.NotNil(t, store)
	info, statErr := os.Stat(wikiDir)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())
}

func TestNewWikiStoreReturnsDirectoryCreationError(t *testing.T) {
	t.Parallel()

	blockingFile := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blockingFile, []byte("content"), 0o600))
	store, err := file.NewWikiStore(&config.Config{
		Paths: config.PathsConfig{WikiDir: filepath.Join(blockingFile, "wiki")},
	})

	require.Error(t, err)
	assert.Nil(t, store)
}

func TestNewWikiStoreDataLayoutCompatibility(t *testing.T) {
	t.Run("fresh store uses Wiki data directory", func(t *testing.T) {
		dataDir := t.TempDir()
		store, err := file.NewWikiStore(&config.Config{Paths: config.PathsConfig{
			WikiDir: filepath.Join(t.TempDir(), "wiki"),
			DataDir: dataDir,
		}})
		require.NoError(t, err)
		require.NoError(t, store.Create(context.Background(), "runbook", "first"))
		require.NoError(t, store.Update(context.Background(), "runbook", "second"))

		_, err = os.Stat(filepath.Join(dataDir, "wiki", "revisions.json"))
		require.NoError(t, err)
		_, err = os.Stat(filepath.Join(dataDir, "docs"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("existing legacy data directory stays in place", func(t *testing.T) {
		dataDir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dataDir, "docs"), 0o750))
		store, err := file.NewWikiStore(&config.Config{Paths: config.PathsConfig{
			WikiDir: filepath.Join(t.TempDir(), "wiki"),
			DataDir: dataDir,
		}})
		require.NoError(t, err)
		require.NoError(t, store.Create(context.Background(), "runbook", "first"))
		require.NoError(t, store.Update(context.Background(), "runbook", "second"))

		_, err = os.Stat(filepath.Join(dataDir, "docs", "revisions.json"))
		require.NoError(t, err)
		_, err = os.Stat(filepath.Join(dataDir, "wiki"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("ambiguous data directories are rejected", func(t *testing.T) {
		dataDir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dataDir, "wiki"), 0o750))
		require.NoError(t, os.Mkdir(filepath.Join(dataDir, "docs"), 0o750))

		store, err := file.NewWikiStore(&config.Config{Paths: config.PathsConfig{
			WikiDir: filepath.Join(t.TempDir(), "wiki"),
			DataDir: dataDir,
		}})
		require.Error(t, err)
		assert.Nil(t, store)
		assert.Contains(t, err.Error(), "both")
	})
}

func TestResolveTokenSecret(t *testing.T) {
	t.Run("auto-generates when file missing", func(t *testing.T) {
		dataDir := t.TempDir()
		authDir := filepath.Join(dataDir, "auth")

		secret, err := file.ResolveTokenSecret(authDir)
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

		secret, err := file.ResolveTokenSecret(authDir)
		require.NoError(t, err)
		assert.Equal(t, []byte("existing-secret"), secret.SigningKey())
	})

	t.Run("regenerates on empty file", func(t *testing.T) {
		dataDir := t.TempDir()
		authDir := filepath.Join(dataDir, "auth")
		require.NoError(t, os.MkdirAll(authDir, 0o700))
		path := filepath.Join(authDir, "token_secret")
		require.NoError(t, os.WriteFile(path, nil, 0o600))

		secret, err := file.ResolveTokenSecret(authDir)
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

		secret, err := file.ResolveTokenSecret(authDir)
		require.NoError(t, err)
		assert.True(t, secret.IsValid())
	})

	t.Run("stable across calls", func(t *testing.T) {
		dataDir := t.TempDir()
		authDir := filepath.Join(dataDir, "auth")

		first, err := file.ResolveTokenSecret(authDir)
		require.NoError(t, err)
		second, err := file.ResolveTokenSecret(authDir)
		require.NoError(t, err)

		assert.Equal(t, first.SigningKey(), second.SigningKey())
	})

	t.Run("directory permissions", func(t *testing.T) {
		dataDir := t.TempDir()
		authDir := filepath.Join(dataDir, "auth")

		_, err := file.ResolveTokenSecret(authDir)
		require.NoError(t, err)

		info, err := os.Stat(authDir)
		require.NoError(t, err)
		if testutil.SupportsPOSIXPermissionBits() {
			assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
		}
	})

	t.Run("concurrent resolve converges to same secret", func(t *testing.T) {
		dataDir := t.TempDir()
		authDir := filepath.Join(dataDir, "auth")

		const goroutines = 10
		secrets := make([][]byte, goroutines)
		errs := make([]error, goroutines)

		var waitGroup sync.WaitGroup
		waitGroup.Add(goroutines)
		for i := range goroutines {
			go func(index int) {
				defer waitGroup.Done()
				secret, err := file.ResolveTokenSecret(authDir)
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
