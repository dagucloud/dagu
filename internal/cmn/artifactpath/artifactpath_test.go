// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package artifactpath_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/artifactpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testTime = time.Date(2026, 9, 15, 14, 32, 7, 0, time.UTC)

// hexSuffix builds a syntactically valid suffix of whatever width the layout
// currently uses, so widening it does not mean rewriting these tables.
func hexSuffix(fill byte) string {
	return strings.Repeat(string(fill), artifactpath.SuffixLen)
}

func TestNewRunDir(t *testing.T) {
	t.Parallel()

	t.Run("Layout", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()

		dir, err := artifactpath.NewRunDir(context.Background(), base, "", "daily-report", "run-1", testTime)
		require.NoError(t, err)
		require.DirExists(t, dir)

		rel, err := filepath.Rel(base, dir)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("2026", "09", "15"), filepath.Dir(rel))

		parsed, ok := artifactpath.ParseRunDirName(filepath.Base(dir))
		require.True(t, ok)
		assert.Equal(t, "143207", parsed.TimeOfDay)
		assert.Equal(t, "daily-report", parsed.DAGName)
		assert.Len(t, parsed.Suffix, artifactpath.SuffixLen)
	})

	t.Run("LocalTimeUsesUTCDay", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()

		// 09:05 JST on the 16th is 00:05 UTC on the 16th; the UTC day wins.
		jst := time.FixedZone("JST", 9*60*60)
		at := time.Date(2026, 9, 16, 9, 5, 0, 0, jst)

		dir, err := artifactpath.NewRunDir(context.Background(), base, "", "tz", "run-1", at)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(base, "2026", "09", "16"), filepath.Dir(dir))
	})

	t.Run("OverrideReplacesBase", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()
		override := t.TempDir()

		dir, err := artifactpath.NewRunDir(context.Background(), base, override, "scoped", "run-1", testTime)
		require.NoError(t, err)
		require.DirExists(t, dir)

		rel, err := filepath.Rel(override, dir)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("2026", "09", "15"), filepath.Dir(rel))
	})

	// Re-deriving a run's directory must land on the same path, so that a
	// component which lost the recorded value cannot mint a second directory.
	t.Run("SameRunResolvesToSamePath", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()

		first, err := artifactpath.NewRunDir(context.Background(), base, "", "same", "run-1", testTime)
		require.NoError(t, err)
		second, err := artifactpath.NewRunDir(context.Background(), base, "", "same", "run-1", testTime)
		require.NoError(t, err)

		assert.Equal(t, first, second)
	})

	t.Run("DistinctRunsInSameSecondDiffer", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()

		first, err := artifactpath.NewRunDir(context.Background(), base, "", "same", "run-1", testTime)
		require.NoError(t, err)
		second, err := artifactpath.NewRunDir(context.Background(), base, "", "same", "run-2", testTime)
		require.NoError(t, err)

		assert.NotEqual(t, first, second)
	})

	// These two run IDs share a 6-character SHA-256 prefix. At that width one
	// would have written into the other's directory.
	t.Run("SeparatesRunIDsSharingAShortHashPrefix", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()

		first, err := artifactpath.NewRunDir(context.Background(), base, "", "same", "run-2162", testTime)
		require.NoError(t, err)
		second, err := artifactpath.NewRunDir(context.Background(), base, "", "same", "run-2198", testTime)
		require.NoError(t, err)

		assert.NotEqual(t, first, second)
	})

	t.Run("RejectsEmptyRunID", func(t *testing.T) {
		t.Parallel()

		_, err := artifactpath.NewRunDir(context.Background(), t.TempDir(), "", "dag", " ", testTime)
		require.Error(t, err)
	})

	t.Run("RejectsMissingRoot", func(t *testing.T) {
		t.Parallel()

		_, err := artifactpath.NewRunDir(context.Background(), "", "", "orphan", "run-1", testTime)
		require.EqualError(t, err, "artifact directory is not configured")
	})

	t.Run("RejectsEmptyDAGName", func(t *testing.T) {
		t.Parallel()

		_, err := artifactpath.NewRunDir(context.Background(), t.TempDir(), "", "  ", "run-1", testTime)
		require.Error(t, err)
	})
}

// An override that expands to nothing must not silently fall back to the global
// root, which would scatter a DAG's artifacts across two trees.
func TestNewRunDirRejectsOverrideExpandingToEmpty(t *testing.T) {
	t.Setenv("EMPTY_ARTIFACT_DIR", "")

	_, err := artifactpath.NewRunDir(
		context.Background(), t.TempDir(), "${EMPTY_ARTIFACT_DIR}", "scoped", "run-1", testTime)
	require.EqualError(t, err, "artifact directory is empty after expansion")
}

// MetaPath must stay in the global tree so one date walk sees every run, even
// when the run directory itself was relocated by artifacts.dir.
func TestMetaPath(t *testing.T) {
	t.Parallel()

	t.Run("SiblingOfRunDir", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()

		dir, err := artifactpath.NewRunDir(context.Background(), base, "", "report", "run-1", testTime)
		require.NoError(t, err)

		meta, ok := artifactpath.MetaPath(base, dir)
		require.True(t, ok)
		assert.Equal(t, dir+artifactpath.MetaSuffix, meta)
	})

	t.Run("OverriddenRunDirKeepsGlobalSidecar", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()
		override := t.TempDir()

		dir, err := artifactpath.NewRunDir(context.Background(), base, override, "report", "run-1", testTime)
		require.NoError(t, err)

		meta, ok := artifactpath.MetaPath(base, dir)
		require.True(t, ok)
		assert.Equal(t, filepath.Join(base, "2026", "09", "15"), filepath.Dir(meta))
		assert.Equal(t, filepath.Base(dir)+artifactpath.MetaSuffix, filepath.Base(meta))
	})

	// A run directory written before this layout has no place in the date tree,
	// which is what keeps pre-existing runs out of the listing.
	t.Run("RejectsLegacyLayout", func(t *testing.T) {
		t.Parallel()

		_, ok := artifactpath.MetaPath("/artifacts",
			filepath.Join("/artifacts", "my-dag", "dag-run_20260915_143207Z_run-1"))
		assert.False(t, ok)
	})

	t.Run("RejectsShallowPath", func(t *testing.T) {
		t.Parallel()

		_, ok := artifactpath.MetaPath("/artifacts", filepath.Join("/", "143207_dag_"+hexSuffix('a')))
		assert.False(t, ok)
	})
}

func TestParseRunDirName(t *testing.T) {
	t.Parallel()

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct{ name, dagName string }{
			{"143207_daily-report_" + hexSuffix('a'), "daily-report"},
			{"000000_report_" + hexSuffix('0'), "report"},
			{"235959_my.dag_" + hexSuffix('f'), "my.dag"},
			{"143207_with_underscores_" + hexSuffix('b'), "with_underscores"},
			{"143207_con_" + hexSuffix('c'), "con"},
			{"143207_trailing._" + hexSuffix('d'), "trailing."},
		}
		for _, tt := range tests {
			parsed, ok := artifactpath.ParseRunDirName(tt.name)
			require.True(t, ok, tt.name)
			assert.Equal(t, tt.dagName, parsed.DAGName, tt.name)
		}
	})

	t.Run("Invalid", func(t *testing.T) {
		t.Parallel()

		for _, name := range []string{
			"",
			"143207",
			"14320_report_" + hexSuffix('a'),  // short time
			"1432o7_report_" + hexSuffix('a'), // non-digit time
			"143207_report_" + strings.Repeat("k", artifactpath.SuffixLen), // non-hex suffix
			"143207__" + hexSuffix('a'),                                    // empty DAG name
			"143207_report-" + hexSuffix('a'),                              // missing suffix separator
			"dag-run_20260915_143207Z_run",                                 // legacy layout
		} {
			_, ok := artifactpath.ParseRunDirName(name)
			assert.False(t, ok, name)
		}
	})

	t.Run("RoundTripsUnsafeName", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()

		dir, err := artifactpath.NewRunDir(context.Background(), base, "", "spaced name/slash", "run-1", testTime)
		require.NoError(t, err)
		require.DirExists(t, dir)

		parsed, ok := artifactpath.ParseRunDirName(filepath.Base(dir))
		require.True(t, ok)
		assert.Equal(t, "spaced_name_slash", parsed.DAGName)
	})
}

func TestMetaNameHelpers(t *testing.T) {
	t.Parallel()

	assert.True(t, artifactpath.IsMetaName("143207_report_"+hexSuffix('a')+artifactpath.MetaSuffix))
	assert.False(t, artifactpath.IsMetaName("143207_report_a7f3c2"))
	assert.Equal(t, "143207_report_"+hexSuffix('a'),
		artifactpath.TrimMetaSuffix("143207_report_"+hexSuffix('a')+artifactpath.MetaSuffix))
}
