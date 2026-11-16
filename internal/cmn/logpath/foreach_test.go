// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package logpath

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForeachStepDir(t *testing.T) {
	t.Parallel()

	dir := ForeachStepDir(filepath.Join("logs", "run"), "each step", nil)
	assert.Equal(t, filepath.Join("logs", "run", "foreach", "each%20step"), dir)
}

// Steps con and con_ keep their items apart on every system, although
// Windows does not store con as written.
func TestForeachStepDirKeepsDeviceNamedStepApart(t *testing.T) {
	t.Parallel()

	logDir := t.TempDir()
	assert.NotEqual(t, ForeachStepDir(logDir, "con", nil), ForeachStepDir(logDir, "con_", nil))
}

// A step whose raw name cleans to another step's name, such as ./b for b,
// keeps its own directory.
func TestForeachStepDirKeepsCleanedNamesApart(t *testing.T) {
	t.Parallel()

	logDir := t.TempDir()
	dir := ForeachStepDir(logDir, "b", nil)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	for _, name := range []string{"./b", "x/../b", "b/."} {
		assert.NotEqual(t, dir, ForeachStepDir(logDir, name, []string{"b", name}), name)
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
	assert.Equal(t, raw, ForeachStepDir(runDir, "con", []string{"con"}))
}

// Foreach steps whose names differ only in characters a file name cannot hold,
// such as a.b and a_b, keep their items apart.
func TestForeachStepDirKeepsSimilarNamesApart(t *testing.T) {
	t.Parallel()

	logDir := t.TempDir()
	assert.NotEqual(t, ForeachStepDir(logDir, "a.b", nil), ForeachStepDir(logDir, "a_b", nil))
	assert.NotEqual(t, ForeachStepDir(logDir, "fetch data", nil), ForeachStepDir(logDir, "fetch_data", nil))
}

// Earlier versions named the directory with SafeName, so a step reads its
// items there when no other step of the run had that name, and never reads
// another step's items.
func TestForeachStepDirReadsLegacyNameItOwns(t *testing.T) {
	t.Parallel()

	logDir := t.TempDir()
	legacy := filepath.Join(logDir, ForeachLogDirName, "fetch_data")
	require.NoError(t, os.MkdirAll(legacy, 0o750))
	assert.Equal(t, legacy, ForeachStepDir(logDir, "fetch data", []string{"fetch data", "parse"}))
	assert.NotEqual(t, legacy, ForeachStepDir(logDir, "fetch data", []string{"fetch data", "fetch_data"}))
	assert.NotEqual(t, legacy, ForeachStepDir(logDir, "fetch.data", []string{"fetch data", "fetch.data"}))
}

// A directory of an earlier version that steps fetch data and fetch_data
// shared is not read even by fetch_data, whose name it has. In a run of this
// version, the directory is fetch_data's own.
func TestForeachStepDirKeepsSharedLegacyDirApart(t *testing.T) {
	t.Parallel()

	siblings := []string{"fetch data", "fetch_data"}
	logDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(logDir, ForeachLogDirName, "fetch_data"), 0o750))
	assert.Empty(t, ForeachStepDir(logDir, "fetch_data", siblings))
	assert.Empty(t, ForeachNestedParent(logDir, "0", "fetch_data", siblings))

	dir := ForeachStepDir(logDir, "fetch_data", nil)
	require.NoError(t, fileutil.MarkSafeKeyDir(filepath.Dir(dir)))
	assert.Equal(t, dir, ForeachStepDir(logDir, "fetch_data", siblings))
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

// A nested foreach step named after a Windows device, whose items earlier
// versions stored under its raw name, is listed under that directory.
func TestForeachNestedParentKeepsRawDeviceName(t *testing.T) {
	t.Parallel()

	stepDir := t.TempDir()
	raw := filepath.Join(stepDir, "0", ForeachLogDirName, "con")
	require.NoError(t, os.MkdirAll(raw, 0o750))
	dir, err := ForeachParentDir(stepDir, ForeachNestedParent(filepath.Join(stepDir, "0"), "0", "con", []string{"con"}))
	require.NoError(t, err)
	assert.Equal(t, raw, dir)
}

func TestForeachItemPathRoundTrip(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "4", ForeachItemPath("", 4))
	parent := ForeachNestedParent(filepath.Join("each", "4"), "4", "inner step", nil)
	assert.Equal(t, "4.inner%20step", parent)
	assert.Equal(t, "4.inner%20step.1", ForeachItemPath(parent, 1))

	stepDir := "each"
	dir, err := ForeachItemDir(stepDir, ForeachItemPath(parent, 1))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("each", "4", "foreach", "inner%20step", "1"), dir)
}
