// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetryPathLeafRoundTrip(t *testing.T) {
	for _, leaf := range []dagrun.RetryLeaf{dagrun.RetryLeafResume, dagrun.RetryLeafReuse} {
		t.Run(string(leaf), func(t *testing.T) {
			path := dagrun.RetryPath{
				Hops: []dagrun.RetryHop{
					{Step: "call-report", RunID: "report-run"},
					{Step: "call-notify", RunID: "notify-run"},
				},
				Leaf: leaf,
			}

			parsed, err := dagrun.ParseRetryPath(path.Encode())
			require.NoError(t, err)
			assert.Equal(t, path, parsed)
		})
	}
}

// Each hop hands the child the next call step to retry; only the hop that
// reaches the last child run exposes the leaf.
func TestRetryPathLeafAdvance(t *testing.T) {
	path := dagrun.RetryPath{
		Hops: []dagrun.RetryHop{
			{Step: "call-report", RunID: "report-run"},
			{Step: "call-notify", RunID: "notify-run"},
		},
		Leaf: dagrun.RetryLeafResume,
	}

	assert.Equal(t, "call-report", path.RootStep())
	assert.Equal(t, "call-notify", path.NextStep())
	assert.Empty(t, path.NextLeaf())

	child := path.Advance()
	hop, ok := child.Current()
	require.True(t, ok)
	assert.Equal(t, dagrun.RetryHop{Step: "call-notify", RunID: "notify-run"}, hop)
	assert.Empty(t, child.NextStep())
	assert.Equal(t, dagrun.RetryLeafResume, child.NextLeaf())
}

func TestRetryPathStepHasNoLeaf(t *testing.T) {
	path := dagrun.RetryPath{
		Hops: []dagrun.RetryHop{{Step: "call-child", RunID: "child-run"}},
		Step: "work",
	}

	parsed, err := dagrun.ParseRetryPath(path.Encode())
	require.NoError(t, err)
	assert.Equal(t, path, parsed)
	assert.Equal(t, "work", parsed.NextStep())
	assert.Empty(t, parsed.NextLeaf())
}

func TestRetryPathRejectsAmbiguousTarget(t *testing.T) {
	hops := []dagrun.RetryHop{{Step: "call-child", RunID: "child-run"}}
	tests := map[string]string{
		"step and leaf": `{"hops":[{"step":"call-child","runId":"child-run"}],"step":"work","leaf":"resume"}`,
		"unknown leaf":  `{"hops":[{"step":"call-child","runId":"child-run"}],"step":"","leaf":"restart"}`,
		"no target":     `{"hops":[{"step":"call-child","runId":"child-run"}],"step":""}`,
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := dagrun.ParseRetryPath(value)
			require.ErrorContains(t, err, "path is incomplete")
		})
	}

	assert.Empty(t, dagrun.RetryPath{Hops: hops, Step: "work", Leaf: dagrun.RetryLeafReuse}.Encode())
	assert.Empty(t, dagrun.RetryPath{Hops: hops, Leaf: "restart"}.Encode())
}
