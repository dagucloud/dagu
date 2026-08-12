// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
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
		Context:       context.Background(),
		Command:       command,
		DAGRepository: persis.NewDAGRepository(warningDAGDefinitionStore{}, persis.DAGRepositoryOptions{}),
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
		Context:          context.Background(),
		Command:          command,
		DAGRepository:    persis.NewDAGRepository(listedDAGDefinitionStore{}, persis.DAGRepositoryOptions{}),
		DAGRunRepository: persis.NewDAGRunRepository(failingRecentDAGRunStore{err: storeErr}, nil, persis.DAGRunRepositoryOptions{}),
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
		Context:       context.Background(),
		Command:       command,
		DAGRepository: persis.NewDAGRepository(listedDAGDefinitionStore{}, persis.DAGRepositoryOptions{}),
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
