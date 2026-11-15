// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package logpath

import (
	"os"
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

// Steps con and con_ keep their items apart on every system, although
// Windows does not store con as written.
func TestForeachStepDirKeepsDeviceNamedStepApart(t *testing.T) {
	t.Parallel()

	log := filepath.Join(t.TempDir(), "step.out")
	assert.NotEqual(t, ForeachStepDir(log, "con"), ForeachStepDir(log, "con_"))
}

// A step whose raw name cleans to another step's name, such as ./b for b,
// keeps its own directory.
func TestForeachStepDirKeepsCleanedNamesApart(t *testing.T) {
	t.Parallel()

	log := filepath.Join(t.TempDir(), "step.out")
	dir := ForeachStepDir(log, "b")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	for _, name := range []string{"./b", "x/../b", "b/."} {
		assert.NotEqual(t, dir, ForeachStepDir(log, name), name)
	}
}

// Earlier versions kept a device name such as con as written, which
// Windows 11 stores, so the items of a foreach step stored under it are
// still found.
func TestForeachStepDirKeepsRawDeviceName(t *testing.T) {
	t.Parallel()

	runDir := t.TempDir()
	raw := filepath.Join(runDir, ForeachLogDirName, "con")
	require.NoError(t, os.MkdirAll(raw, 0o750))
	assert.Equal(t, raw, ForeachStepDir(filepath.Join(runDir, "con.out"), "con"))
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
