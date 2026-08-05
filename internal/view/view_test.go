// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package view_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/view"
)

func validView() *view.View {
	return &view.View{
		ID:           "id-1",
		Name:         "My View",
		Type:         view.TypeKanban,
		IntervalDays: 3,
		Columns:      view.DefaultColumns(),
	}
}

func TestView_Validate_OK(t *testing.T) {
	require.NoError(t, validView().Validate())
}

func TestView_ValidateWorkflow(t *testing.T) {
	v := &view.View{
		Name:           "Production workflows",
		Type:           view.TypeWorkflow,
		WorkspaceScope: view.WorkspaceScopeWorkspace,
		Workspace:      "production",
		SortField:      view.WorkflowSortNextRun,
		SortOrder:      view.SortOrderDescending,
		Pinned:         true,
	}
	v.Normalize()

	require.NoError(t, v.Validate())
	assert.Equal(t, view.MinIntervalDays, v.IntervalDays)
	assert.Nil(t, v.Columns)
	assert.True(t, v.Pinned)
}

func TestView_Validate_Errors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*view.View)
		want   error
	}{
		{"empty name", func(v *view.View) { v.Name = "" }, view.ErrInvalidName},
		{"name too long", func(v *view.View) { v.Name = strings.Repeat("a", view.MaxNameLength+1) }, view.ErrNameTooLong},
		{"dagName too long", func(v *view.View) { v.DAGName = strings.Repeat("d", view.MaxDAGNameLength+1) }, view.ErrDAGNameTooLong},
		{"interval zero", func(v *view.View) { v.IntervalDays = 0 }, view.ErrInvalidInterval},
		{"interval too large", func(v *view.View) { v.IntervalDays = view.MaxIntervalDays + 1 }, view.ErrInvalidInterval},
		{"too many labels", func(v *view.View) { v.Labels = make([]string, view.MaxLabels+1) }, view.ErrTooManyLabels},
		{"unknown type", func(v *view.View) { v.Type = "timeline" }, view.ErrInvalidType},
		{"unknown column", func(v *view.View) { v.Columns = []string{"running", "unknown"} }, view.ErrInvalidColumns},
		{"duplicate column", func(v *view.View) { v.Columns = []string{"running", "running"} }, view.ErrInvalidColumns},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := validView()
			tt.mutate(v)
			assert.ErrorIs(t, v.Validate(), tt.want)
		})
	}
}

func TestView_Normalize(t *testing.T) {
	v := &view.View{
		Name:         "  spaced  ",
		Type:         "",
		Workspace:    "  ws  ",
		DAGName:      "  dag  ",
		Labels:       []string{" a ", "", "  ", "b", strings.Repeat("x", view.MaxLabelLength+1)},
		IntervalDays: 5,
	}
	v.Normalize()

	assert.Equal(t, "spaced", v.Name)
	assert.Equal(t, view.TypeKanban, v.Type, "empty type defaults to kanban")
	assert.Equal(t, "ws", v.Workspace)
	assert.Equal(t, "dag", v.DAGName)
	assert.Equal(t, []string{"a", "b"}, v.Labels, "empty and oversized labels are dropped")
	assert.Equal(t, 5, v.IntervalDays)
	assert.Equal(t, view.DefaultColumns(), v.Columns)
}

func TestView_StorageRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	original := &view.View{
		ID:           "id-1",
		Name:         "N",
		Type:         view.TypeKanban,
		Workspace:    "ws",
		Labels:       []string{"a", "b=c"},
		DAGName:      "etl",
		IntervalDays: 7,
		Columns:      []string{view.ColumnRunning, view.ColumnFailed},
		Pinned:       true,
		CreatedBy:    "alice",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	got := original.ToStorage().ToView()
	assert.Equal(t, original, got)
}

func TestView_WorkflowStorageRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	original := &view.View{
		ID:             "workflow-id",
		Name:           "Production workflows",
		Type:           view.TypeWorkflow,
		WorkspaceScope: view.WorkspaceScopeDefault,
		SortField:      view.WorkflowSortName,
		SortOrder:      view.SortOrderAscending,
		Default:        true,
		Pinned:         true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	assert.Equal(t, original, original.ToStorage().ToView())
}

func TestView_StoredViewWithoutColumnsUsesDefaultLayout(t *testing.T) {
	stored := &view.ViewForStorage{ID: "legacy", Name: "Legacy"}

	assert.Equal(t, view.DefaultColumns(), stored.ToView().Columns)
}
