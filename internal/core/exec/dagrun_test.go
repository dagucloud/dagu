// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListDAGRunStatusesOptions(t *testing.T) {
	from := exec.NewUTC(time.Now().Add(-24 * time.Hour))
	to := exec.NewUTC(time.Now())
	statuses := []core.Status{core.Succeeded, core.Failed}

	opts := exec.ListDAGRunStatusesOptions{}

	// Apply options
	exec.WithFrom(from)(&opts)
	exec.WithTo(to)(&opts)
	exec.WithStatuses(statuses)(&opts)
	exec.WithExactName("test-dag")(&opts)
	exec.WithName("partial-name")(&opts)
	exec.WithDAGRunID("run-123")(&opts)
	exec.WithAllHistory()(&opts)

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
	rootDAGRun := &exec.DAGRunRef{
		Name: "root-dag",
		ID:   "root-run-123",
	}

	opts := exec.NewDAGRunAttemptOptions{
		RootDAGRun: rootDAGRun,
		Retry:      true,
	}

	assert.Equal(t, rootDAGRun, opts.RootDAGRun)
	assert.True(t, opts.Retry)
}

func TestParseDAGRunRef(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantID   string
		wantErr  bool
	}{
		{name: "Valid", input: "my-dag:01H8XGJWBWBAQ4ZPQ", wantName: "my-dag", wantID: "01H8XGJWBWBAQ4ZPQ"},
		{name: "PathName", input: "team/my-dag.yaml:run-1", wantName: "team/my-dag.yaml", wantID: "run-1"},
		{name: "MissingDelimiter", input: "my-dag", wantErr: true},
		{name: "Empty", input: "", wantErr: true},
		{name: "DelimiterOnly", input: ":", wantErr: true},
		{name: "EmptyRunID", input: "my-dag:", wantErr: true},
		{name: "EmptyName", input: ":run-1", wantErr: true},
		{name: "ExtraDelimiter", input: "my-dag:run:1", wantErr: true},
		{name: "InvalidRunIDChar", input: "my-dag:run 1", wantErr: true},
		{name: "RunIDTooLong", input: "my-dag:" + strings.Repeat("a", 65), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := exec.ParseDAGRunRef(tt.input)
			if tt.wantErr {
				require.ErrorIs(t, err, exec.ErrInvalidRunRefFormat)
				assert.Equal(t, exec.DAGRunRef{}, ref)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, ref.Name)
			assert.Equal(t, tt.wantID, ref.ID)
		})
	}
}
