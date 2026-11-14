// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendLines(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "events.jsonl")

	require.NoError(t, fileutil.AppendLines(path, 0o600, []byte("one"), []byte("two")))
	// A crash cut the next line short.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"cut`)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, fileutil.AppendLines(path, 0o600, []byte("three")))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\n{\"cut\nthree\n", string(data))
}
