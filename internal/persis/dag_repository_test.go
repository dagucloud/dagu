// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dagDefinitionStoreStub struct {
	DAGDefinitionStore
	definition  DAGDefinition
	definitions map[string]DAGDefinition
	catalog     DAGCatalog
	update      func(context.Context, string, []byte) error
}

func (s dagDefinitionStoreStub) Get(_ context.Context, id string) (DAGDefinition, error) {
	if s.definitions != nil {
		return s.definitions[id], nil
	}
	return s.definition, nil
}

func (s dagDefinitionStoreStub) Catalog(context.Context) (DAGCatalog, error) {
	return s.catalog, nil
}

func (s dagDefinitionStoreStub) Update(ctx context.Context, id string, source []byte) error {
	if s.update == nil {
		return nil
	}
	return s.update(ctx, id, source)
}

func TestDAGRepositoryLoadsDefinitionWithoutSourcePath(t *testing.T) {
	repository := NewDAGRepository(dagDefinitionStoreStub{
		definition: DAGDefinition{
			ID:     "in-memory",
			Source: []byte("name: in-memory\nsteps: []\n"),
		},
	}, DAGRepositoryOptions{})

	dag, err := repository.GetDetails(context.Background(), "in-memory", DAGLoadOptions{})
	require.NoError(t, err)
	assert.Equal(t, "in-memory", dag.Name)
}

func TestDAGRepositoryLoadsStoredSourceWithAuthoredPath(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "resolved-target-name-that-is-not-the-entry.yaml")
	repository := NewDAGRepository(dagDefinitionStoreStub{
		definition: DAGDefinition{
			ID:         "stored",
			SourcePath: sourcePath,
			Source:     []byte("steps: []\n"),
		},
	}, DAGRepositoryOptions{})

	dag, err := repository.GetDetails(context.Background(), "stored", DAGLoadOptions{})
	require.NoError(t, err)
	assert.Equal(t, "stored", dag.Name)
	assert.Equal(t, sourcePath, dag.Location)
	assert.Equal(t, sourcePath, dag.SourceFile)
	assert.Equal(t, filepath.Dir(sourcePath), dag.WorkingDir)
}

func TestDAGRepositoryUpdateValidatesFromStoredSourcePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "resolved-target-name-that-is-not-the-entry.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "params.schema.json"), []byte(`{"type":"object"}`), 0o600))

	updated := false
	repository := NewDAGRepository(dagDefinitionStoreStub{
		definition: DAGDefinition{ID: "stored", SourcePath: sourcePath},
		update: func(_ context.Context, id string, source []byte) error {
			updated = true
			assert.Equal(t, "stored", id)
			assert.Contains(t, string(source), "params.schema.json")
			return nil
		},
	}, DAGRepositoryOptions{})

	err := repository.UpdateSpec(context.Background(), "stored", []byte(`
params:
  schema: params.schema.json
steps: []
`))
	require.NoError(t, err)
	assert.True(t, updated)
}

func TestDAGRepositoryListsByBackendIdentity(t *testing.T) {
	repository := NewDAGRepository(dagDefinitionStoreStub{
		catalog: DAGCatalog{Items: []DAGListItem{
			{ID: "beta-file", DAG: &ir.DAG{Name: "unrelated"}, Suspended: true},
			{ID: "alpha-file", DAG: &ir.DAG{Name: "also-unrelated"}},
		}},
	}, DAGRepositoryOptions{})

	result, issues, err := repository.List(context.Background(), DAGListOptions{Name: "file"})
	require.NoError(t, err)
	assert.Empty(t, issues)
	require.Len(t, result.Items, 2)
	assert.Equal(t, "alpha-file", result.Items[0].ID)
	assert.Equal(t, "beta-file", result.Items[1].ID)
	assert.True(t, result.Items[1].Suspended)
}

// Pinned DAGs come first in every sort, filters still apply to them, and
// pagination counts both parts together.
func TestDAGRepositoryListsPinnedFirst(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nextRuns := map[string]time.Time{
		"beta":  at.Add(2 * time.Hour),
		"gamma": at.Add(time.Hour),
	}
	repository := NewDAGRepository(dagDefinitionStoreStub{
		catalog: DAGCatalog{Items: []DAGListItem{
			{ID: "delta", DAG: &ir.DAG{Name: "delta"}},
			{ID: "beta", DAG: &ir.DAG{Name: "beta"}},
			{ID: "alpha", DAG: &ir.DAG{Name: "alpha"}},
			{ID: "gamma", DAG: &ir.DAG{Name: "gamma"}},
		}},
	}, DAGRepositoryOptions{})
	pins := func(ids ...string) map[string]struct{} {
		set := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			set[id] = struct{}{}
		}
		return set
	}
	page := func(number, size int) *pagination.Paginator {
		paginator := pagination.NewPaginator(number, size)
		return &paginator
	}

	tests := []struct {
		name string
		opts DAGListOptions
		want []string
	}{
		{
			name: "NameAscending",
			opts: DAGListOptions{PinnedIDs: pins("gamma")},
			want: []string{"gamma", "alpha", "beta", "delta"},
		},
		{
			name: "NameDescending",
			opts: DAGListOptions{Order: "desc", PinnedIDs: pins("alpha", "gamma")},
			want: []string{"gamma", "alpha", "delta", "beta"},
		},
		{
			name: "NextRunKeepsPinnedWithoutNextRunFirst",
			opts: DAGListOptions{
				Sort:      "nextRun",
				Time:      &at,
				PinnedIDs: pins("alpha"),
				NextRunProjection: func(dag *ir.DAG, _ time.Time) time.Time {
					return nextRuns[dag.Name]
				},
			},
			want: []string{"alpha", "gamma", "beta", "delta"},
		},
		{
			name: "SecondPageAfterPinned",
			opts: DAGListOptions{Paginator: page(2, 2), PinnedIDs: pins("delta", "gamma")},
			want: []string{"alpha", "beta"},
		},
		{
			name: "FilterExcludesPinned",
			opts: DAGListOptions{Name: "bet", PinnedIDs: pins("gamma")},
			want: []string{"beta"},
		},
		{
			name: "StalePinIgnored",
			opts: DAGListOptions{PinnedIDs: pins("removed")},
			want: []string{"alpha", "beta", "delta", "gamma"},
		},
		{
			name: "NoPins",
			opts: DAGListOptions{},
			want: []string{"alpha", "beta", "delta", "gamma"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _, err := repository.List(context.Background(), tt.opts)
			require.NoError(t, err)
			ids := make([]string, 0, len(result.Items))
			for _, item := range result.Items {
				ids = append(ids, item.ID)
			}
			assert.Equal(t, tt.want, ids)
			if tt.opts.Paginator != nil {
				assert.Equal(t, 4, result.TotalCount)
			}
		})
	}
}

func TestDAGRepositorySearchOrdersBackendResultsByIdentity(t *testing.T) {
	repository := NewDAGRepository(dagDefinitionStoreStub{
		definitions: map[string]DAGDefinition{
			"alpha": {ID: "alpha", Source: []byte("needle alpha")},
			"beta":  {ID: "beta", Source: []byte("needle beta")},
		},
		catalog: DAGCatalog{Items: []DAGListItem{
			{ID: "beta", DAG: &ir.DAG{Name: "Beta"}},
			{ID: "alpha", DAG: &ir.DAG{Name: "Alpha"}},
		}},
	}, DAGRepositoryOptions{})

	result, issues, err := repository.SearchCursor(context.Background(), DAGSearchOptions{
		Query: "needle",
		Limit: 1,
	})
	require.NoError(t, err)
	assert.Empty(t, issues)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "alpha", result.Items[0].FileName)
	assert.True(t, result.HasMore)
}
