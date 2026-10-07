// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package logpath

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForeachStepDir(t *testing.T) {
	t.Parallel()

	dir := ForeachStepDir(filepath.Join("logs", "run", "each step.out"), "each step")
	assert.Equal(t, filepath.Join("logs", "run", "foreach", "each_step"), dir)
}

func TestForeachItemDir(t *testing.T) {
	t.Parallel()

	stepDir := filepath.Join("logs", "run", "foreach", "each")
	tests := []struct {
		path string
		want string
	}{
		{"0", filepath.Join(stepDir, "0")},
		{"12", filepath.Join(stepDir, "12")},
		{"0.inner.3", filepath.Join(stepDir, "0", "foreach", "inner", "3")},
		{"1.a-b_c.0.deep.2", filepath.Join(stepDir, "1", "foreach", "a-b_c", "0", "foreach", "deep", "2")},
	}
	for _, tt := range tests {
		got, err := ForeachItemDir(stepDir, tt.path)
		require.NoError(t, err, tt.path)
		assert.Equal(t, tt.want, got, tt.path)
	}

	for _, path := range []string{"", "-1", "01", "a", "0.inner", "0..1", "0.in/ner.1", "0.inner.x", "0.inner.1.", "../0"} {
		_, err := ForeachItemDir(stepDir, path)
		assert.Error(t, err, path)
	}
}

func TestForeachParentDir(t *testing.T) {
	t.Parallel()

	stepDir := filepath.Join("logs", "run", "foreach", "each")
	got, err := ForeachParentDir(stepDir, "")
	require.NoError(t, err)
	assert.Equal(t, stepDir, got)

	got, err = ForeachParentDir(stepDir, "2.inner")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(stepDir, "2", "foreach", "inner"), got)

	for _, parent := range []string{"2", "2.inner.0", "x.inner", "2.in ner"} {
		_, err := ForeachParentDir(stepDir, parent)
		assert.Error(t, err, parent)
	}
}

func TestForeachItemPathRoundTrip(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "4", ForeachItemPath("", 4))
	parent := ForeachNestedParent("4", "inner step")
	assert.Equal(t, "4.inner_step", parent)
	assert.Equal(t, "4.inner_step.1", ForeachItemPath(parent, 1))

	stepDir := "each"
	dir, err := ForeachItemDir(stepDir, ForeachItemPath(parent, 1))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("each", "4", "foreach", "inner_step", "1"), dir)
}
