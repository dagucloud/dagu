// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
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
