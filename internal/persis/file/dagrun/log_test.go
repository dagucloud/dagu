// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenLog(t *testing.T) {
	store := NewStore(t.TempDir())
	path := filepath.Join(t.TempDir(), "step.log")
	content := []byte{0xff, 0, 'a', '\r', '\n'}
	require.NoError(t, os.WriteFile(path, content, 0o600))
	reader, err := store.OpenLog(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = file.WriteString("later output")
	require.NoError(t, err)
	require.NoError(t, file.Close())
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, content, got)
	require.NoError(t, reader.Close())

	_, err = store.OpenLog(t.Context(), filepath.Join(t.TempDir(), "missing"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = store.OpenLog(t.Context(), t.TempDir())
	require.ErrorContains(t, err, "not a regular file")
}

func TestOpenLogCanceled(t *testing.T) {
	store := NewStore(t.TempDir())
	path := filepath.Join(t.TempDir(), "step.log")
	require.NoError(t, os.WriteFile(path, []byte("output"), 0o600))
	ctx, cancel := context.WithCancel(t.Context())
	reader, err := store.OpenLog(ctx, path)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	cancel()
	_, err = io.ReadAll(reader)
	require.ErrorIs(t, err, context.Canceled)
	_, err = store.OpenLog(ctx, path)
	require.ErrorIs(t, err, context.Canceled)
}

func TestOpenLogTruncated(t *testing.T) {
	store := NewStore(t.TempDir())
	path := filepath.Join(t.TempDir(), "step.log")
	require.NoError(t, os.WriteFile(path, []byte("original"), 0o600))
	reader, err := store.OpenLog(t.Context(), path)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	require.NoError(t, os.Truncate(path, 0))
	_, err = io.ReadAll(reader)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}
