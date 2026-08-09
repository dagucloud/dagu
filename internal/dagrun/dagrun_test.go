// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListDAGRunStatusesOptions(t *testing.T) {
	from := dagrun.NewUTC(time.Now().Add(-24 * time.Hour))
	to := dagrun.NewUTC(time.Now())
	statuses := []ir.Status{ir.Succeeded, ir.Failed}

	opts := dagrun.ListDAGRunStatusesOptions{}

	// Apply options
	dagrun.WithFrom(from)(&opts)
	dagrun.WithTo(to)(&opts)
	dagrun.WithStatuses(statuses)(&opts)
	dagrun.WithExactName("test-dag")(&opts)
	dagrun.WithName("partial-name")(&opts)
	dagrun.WithDAGRunID("run-123")(&opts)
	dagrun.WithAllHistory()(&opts)

	// Verify options were set correctly
	assert.Equal(t, from, opts.From)
	assert.Equal(t, to, opts.To)
	assert.Equal(t, statuses, opts.Statuses)
	assert.Equal(t, "test-dag", opts.ExactName)
	assert.Equal(t, "partial-name", opts.Name)
	assert.Equal(t, "run-123", opts.DAGRunID)
	assert.True(t, opts.AllHistory)
}

func TestNewDAGRunAttemptOptions(t *testing.T) {
	rootDAGRun := &ir.DAGRunRef{
		Name: "root-dag",
		ID:   "root-run-123",
	}

	opts := dagrun.NewDAGRunAttemptOptions{
		RootDAGRun: rootDAGRun,
		Retry:      true,
	}

	assert.Equal(t, rootDAGRun, opts.RootDAGRun)
	assert.True(t, opts.Retry)
}

func TestNormalizeStateValueCompactsJSON(t *testing.T) {
	value, err := dagrun.NormalizeStateValue([]byte(`{ "b": 2, "a": 1 }`))
	require.NoError(t, err)
	assert.Equal(t, `{"a":1,"b":2}`, string(value))

	_, err = dagrun.NormalizeStateValue([]byte(`{`))
	require.ErrorIs(t, err, dagrun.ErrInvalidStateValue)
}

func TestNormalizeStateValueRejectsNormalizedValueOverLimit(t *testing.T) {
	raw := []byte(`"` + strings.Repeat("<", dagrun.MaxStateValueBytes/6+1) + `"`)
	assert.Less(t, len(raw), dagrun.MaxStateValueBytes)

	_, err := dagrun.NormalizeStateValue(raw)
	require.ErrorIs(t, err, dagrun.ErrStateValueTooLarge)
}

func TestNormalizeStateValuePreservesNumericPrecision(t *testing.T) {
	value, err := dagrun.NormalizeStateValue([]byte(`{"id":9007199254740993,"decimal":1.2300}`))
	require.NoError(t, err)
	assert.Equal(t, `{"decimal":1.2300,"id":9007199254740993}`, string(value))
}
