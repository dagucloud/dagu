// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package projectrecord

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteRead(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "projects", "weather")
	record := Record{ID: "0f8e2c1a-5b7d-4e2f-9a61-3c4d5e6f7a8b", Name: "weather"}

	require.NoError(t, Write(dir, record))
	got, err := Read(dir)
	require.NoError(t, err)
	assert.Equal(t, record, got)

	// Writing the same record again is a no-op.
	require.NoError(t, Write(dir, record))
}

// A state directory left by a deleted project is never taken over by a new
// project of the same name, nor relabeled under another name.
func TestWriteRefusesOtherProject(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "weather")
	record := Record{ID: "0f8e2c1a-5b7d-4e2f-9a61-3c4d5e6f7a8b", Name: "weather"}
	require.NoError(t, Write(dir, record))

	err := Write(dir, Record{ID: "6a1b2c3d-4e5f-4a7b-8c9d-0e1f2a3b4c5d", Name: "weather"})
	assert.ErrorIs(t, err, ErrOtherProject)
	err = Write(dir, Record{ID: record.ID, Name: "climate"})
	assert.ErrorIs(t, err, ErrOtherProject)

	got, err := Read(dir)
	require.NoError(t, err)
	assert.Equal(t, record, got)
}

func TestWriteConcurrentProjects(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "weather")

	const writers = 8
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			errs <- Write(dir, Record{ID: fmt.Sprintf("project-%d", i), Name: "weather"})
		})
	}
	wg.Wait()
	close(errs)

	var recorded int
	for err := range errs {
		if err == nil {
			recorded++
			continue
		}
		assert.ErrorIs(t, err, ErrOtherProject)
	}
	assert.Equal(t, 1, recorded)
}

func TestReadMissing(t *testing.T) {
	t.Parallel()

	_, err := Read(t.TempDir())
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestReadRejectsIncompleteRecord(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"NoID":    `{"name":"weather"}`,
		"NoName":  `{"id":"0f8e2c1a-5b7d-4e2f-9a61-3c4d5e6f7a8b"}`,
		"NotJSON": `id: weather`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, fileName), []byte(content), 0o600))

			_, err := Read(dir)
			assert.Error(t, err)
		})
	}
}

func TestWriteRejectsIncompleteRecord(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "weather")

	require.Error(t, Write(dir, Record{Name: "weather"}))
	require.Error(t, Write(dir, Record{ID: "0f8e2c1a-5b7d-4e2f-9a61-3c4d5e6f7a8b"}))
	assert.NoDirExists(t, dir)
}
