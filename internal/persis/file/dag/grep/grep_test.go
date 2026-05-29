// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package grep

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/require"
)

func TestGrep(t *testing.T) {
	t.Parallel()
	wd, _ := os.Getwd()
	dir := filepath.Join(wd, "/testdata")
	for _, tc := range []struct {
		Name    string
		File    string
		Pattern string
		Opts    GrepOptions
		Want    []*exec.Match
		IsErr   bool
	}{
		{
			Name:    "simple",
			File:    filepath.Join(dir, "test.txt"),
			Pattern: "b",
			Want: []*exec.Match{
				{
					LineNumber: 2,
					StartLine:  2,
					Line:       "bb",
				}},
		},
		{
			Name:    "regexp",
			File:    filepath.Join(dir, "test.txt"),
			Pattern: "^b.",
			Opts: GrepOptions{
				IsRegexp: true,
			},
			Want: []*exec.Match{
				{
					LineNumber: 2,
					StartLine:  2,
					Line:       "bb",
				}},
		},
		{
			Name:    "before",
			File:    filepath.Join(dir, "test.txt"),
			Pattern: "b",
			Opts: GrepOptions{
				Before: 1,
			},
			Want: []*exec.Match{
				{
					LineNumber: 2,
					StartLine:  1,
					Line:       "aa\nbb",
				}},
		},
		{
			Name:    "before+after",
			File:    filepath.Join(dir, "test.txt"),
			Pattern: "cc",
			Opts: GrepOptions{
				Before: 2,
				After:  2,
			},
			Want: []*exec.Match{
				{
					LineNumber: 3,
					StartLine:  1,
					Line:       "aa\nbb\ncc\ndd\nee",
				}},
		},
		{
			Name:    "before+after,firstline",
			File:    filepath.Join(dir, "test.txt"),
			Pattern: "aa",
			Opts: GrepOptions{
				Before: 1,
				After:  1,
			},
			Want: []*exec.Match{
				{
					LineNumber: 1,
					StartLine:  1,
					Line:       "aa\nbb",
				}},
		},
		{
			Name:    "before+after,lastline",
			File:    filepath.Join(dir, "test.txt"),
			Pattern: "ee",
			Opts: GrepOptions{
				Before: 1,
				After:  1,
			},
			Want: []*exec.Match{
				{
					LineNumber: 5,
					StartLine:  4,
					Line:       "dd\nee",
				}},
		},
		{
			Name:    "no match",
			File:    filepath.Join(dir, "test.txt"),
			Pattern: "no match text",
			IsErr:   true,
		},
		{
			Name:    "no file",
			File:    filepath.Join(dir, "dummy.txt"),
			Pattern: "aa",
			IsErr:   true,
		},
		{
			Name:    "no pattern",
			File:    filepath.Join(dir, "test.txt"),
			Pattern: "",
			IsErr:   true,
		},
		{
			Name:    "invalid regexp",
			File:    filepath.Join(dir, "test.txt"),
			Pattern: "(aa",
			Opts: GrepOptions{
				IsRegexp: true,
			},
			IsErr: true,
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			dat, _ := os.ReadFile(tc.File)
			ret, err := Grep(dat, tc.Pattern, tc.Opts)
			if tc.IsErr {
				require.Empty(t, ret)
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.Want, ret)
		})
	}
}

func TestGrepWindow(t *testing.T) {
	t.Parallel()

	dat := []byte("aa\nneedle 1\nbb\nneedle 2\ncc\nneedle 3\ndd")
	result, err := GrepWindow(dat, "needle", GrepOptions{
		Before: 1,
		After:  1,
		Limit:  2,
	})
	require.NoError(t, err)
	require.Len(t, result.Matches, 2)
	require.True(t, result.HasMore)
	require.Equal(t, 2, result.NextOffset)
	require.Equal(t, "aa\nneedle 1\nbb", result.Matches[0].Line)
	require.Equal(t, "bb\nneedle 2\ncc", result.Matches[1].Line)

	next, err := GrepWindow(dat, "needle", GrepOptions{
		Before: 1,
		After:  1,
		Offset: result.NextOffset,
		Limit:  2,
	})
	require.NoError(t, err)
	require.Len(t, next.Matches, 1)
	require.False(t, next.HasMore)
	require.Equal(t, "cc\nneedle 3\ndd", next.Matches[0].Line)
}
