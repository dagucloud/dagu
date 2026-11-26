// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type warningDAGDefinitionStore struct {
	persis.DAGDefinitionStore
}

func (warningDAGDefinitionStore) Catalog(context.Context) (persis.DAGCatalog, error) {
	return persis.DAGCatalog{Issues: []string{"catalog warning"}}, nil
}

type listedDAGDefinitionStore struct {
	persis.DAGDefinitionStore
}

func (listedDAGDefinitionStore) Catalog(context.Context) (persis.DAGCatalog, error) {
	return persis.DAGCatalog{Items: []persis.DAGListItem{{
		ID:  "daily.yaml",
		DAG: &ir.DAG{Name: "daily"},
	}}}, nil
}

type failingRecentDAGRunStore struct {
	testutil.DAGRunStoreStub
	err error
}

func (s failingRecentDAGRunStore) RecentStatuses(context.Context, string, int) ([]ir.DAGRunStatus, error) {
	return nil, s.err
}

func TestRunLsWritesWarningsToCommandErrorStream(t *testing.T) {
	t.Parallel()

	command := Ls()
	command.SetOut(io.Discard)
	var stderr bytes.Buffer
	command.SetErr(&stderr)

	err := runLs(&Context{
		Context: context.Background(),
		Command: command,
		Persistence: Persistence{
			DAGRepository: persis.NewDAGRepository(warningDAGDefinitionStore{}, persis.DAGRepositoryOptions{}),
		},
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "warning: catalog warning\n", stderr.String())
}

func TestRunLsWarnsAndKeepsRowsWhenRecentHistoryFails(t *testing.T) {
	t.Parallel()

	command := Ls()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	require.NoError(t, command.Flags().Set("history", "true"))

	storeErr := errors.New("storage unavailable")
	err := runLs(&Context{
		Context: context.Background(),
		Command: command,
		Persistence: Persistence{
			DAGRepository:    persis.NewDAGRepository(listedDAGDefinitionStore{}, persis.DAGRepositoryOptions{}),
			DAGRunRepository: persis.NewDAGRunRepository(failingRecentDAGRunStore{err: storeErr}, nil, persis.DAGRunRepositoryOptions{}),
		},
	}, nil)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "daily")
	assert.Contains(t, stdout.String(), "-")
	assert.Contains(t, stderr.String(), "warning: failed to load recent DAG-run history for daily")
	assert.Contains(t, stderr.String(), storeErr.Error())
}

func TestRunLsRequiresDAGRunRepositoryForHistory(t *testing.T) {
	t.Parallel()

	command := Ls()
	command.SetOut(io.Discard)
	require.NoError(t, command.Flags().Set("history", "true"))

	err := runLs(&Context{
		Context: context.Background(),
		Command: command,
		Persistence: Persistence{
			DAGRepository: persis.NewDAGRepository(listedDAGDefinitionStore{}, persis.DAGRepositoryOptions{}),
		},
	}, nil)
	require.EqualError(t, err, "DAG-run repository is not available")
}

func TestSortLsRowsByLastRun(t *testing.T) {
	t.Parallel()

	older := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	rows := []lsRow{
		{dag: &ir.DAG{Name: "never-b"}},
		{dag: &ir.DAG{Name: "older"}, lastTime: older},
		{dag: &ir.DAG{Name: "newer"}, lastTime: newer},
		{dag: &ir.DAG{Name: "never-a"}},
	}

	sortLsRowsByLastRun(rows, false)
	assert.Equal(t, []string{"newer", "older", "never-a", "never-b"}, lsRowNames(rows))

	sortLsRowsByLastRun(rows, true)
	assert.Equal(t, []string{"older", "newer", "never-b", "never-a"}, lsRowNames(rows))
}

func lsRowNames(rows []lsRow) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.dag.Name)
	}
	return names
}

func TestRenderLsTableNextRunLocal(t *testing.T) {
	t.Parallel()

	nextRun := time.Date(2026, time.November, 30, 11, 0, 0, 0, time.UTC)
	cet := time.FixedZone("CET", 3600)

	cases := []struct {
		name    string
		nextRun time.Time
		loc     *time.Location
		want    string
	}{
		{
			name:    "ConfiguredLocation",
			nextRun: nextRun,
			loc:     cet,
			want:    "2026-11-30T12:00:00+01:00",
		},
		{
			name:    "NilLocationUsesUTC",
			nextRun: nextRun,
			loc:     nil,
			want:    "2026-11-30T11:00:00Z",
		},
		{
			name:    "UTCLocationUsesZ",
			nextRun: nextRun,
			loc:     time.UTC,
			want:    "2026-11-30T11:00:00Z",
		},
		{
			name:    "ZeroNextRunStaysDash",
			nextRun: time.Time{},
			loc:     cet,
			want:    "-",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			rows := []lsRow{{dag: &ir.DAG{Name: "daily"}, nextRun: tc.nextRun}}
			require.NoError(t, renderLsTable(&out, rows, true, false, false, tc.loc))

			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			require.Len(t, lines, 2)
			fields := strings.Fields(lines[1])
			require.Equal(t, []string{"daily", tc.want}, fields)
		})
	}
}
