// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package view_test

import (
	"strconv"
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

func TestView_ValidateWorkflowRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*view.View)
		want   error
	}{
		{"all scope with workspace", func(v *view.View) { v.Workspace = "production" }, view.ErrInvalidWorkspaceScope},
		{"workspace scope without workspace", func(v *view.View) { v.WorkspaceScope = view.WorkspaceScopeWorkspace }, view.ErrInvalidWorkspaceScope},
		{"unknown sort field", func(v *view.View) { v.SortField = "created" }, view.ErrInvalidSortField},
		{"unknown sort order", func(v *view.View) { v.SortOrder = "descending" }, view.ErrInvalidSortOrder},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &view.View{Name: "workflow", Type: view.TypeWorkflow}
			v.Normalize()
			tt.mutate(v)
			assert.ErrorIs(t, v.Validate(), tt.want)
		})
	}
}

func TestView_ValidateRun(t *testing.T) {
	v := &view.View{
		Name:           "Production runs",
		Type:           view.TypeRun,
		WorkspaceScope: view.WorkspaceScopeWorkspace,
		Workspace:      "production",
		DAGRunID:       "019df6cf-0127-7340-bd96-d51bc1453045",
		RunStatus:      "5",
		DateMode:       view.DateModePreset,
		DatePreset:     view.DatePresetLast7Days,
		SpecificPeriod: view.SpecificPeriodDate,
		SpecificValue:  "2026-09-15",
		FromDate:       "2026-09-15T00:00",
		ToDate:         "2026-09-15T23:59",
		Pinned:         true,
	}
	v.Normalize()

	require.NoError(t, v.Validate())
	assert.Equal(t, view.MinIntervalDays, v.IntervalDays)
	assert.Nil(t, v.Columns)
	assert.True(t, v.Pinned)
}

func TestView_ValidateRunRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*view.View)
		want   error
	}{
		{"all scope with workspace", func(v *view.View) { v.Workspace = "production" }, view.ErrInvalidWorkspaceScope},
		{"workspace scope without workspace", func(v *view.View) { v.WorkspaceScope = view.WorkspaceScopeWorkspace }, view.ErrInvalidWorkspaceScope},
		{"dagRunId too long", func(v *view.View) { v.DAGRunID = strings.Repeat("r", view.MaxDAGRunIDLength+1) }, view.ErrDAGRunIDTooLong},
		{"runStatus not a status", func(v *view.View) { v.RunStatus = "failed" }, view.ErrInvalidRunStatus},
		{"runStatus out of range", func(v *view.View) { v.RunStatus = "9" }, view.ErrInvalidRunStatus},
		{"runStatus too long", func(v *view.View) { v.RunStatus = strings.Repeat("s", view.MaxRunStatusLength+1) }, view.ErrRunStatusTooLong},
		{"specific value not a date", func(v *view.View) { v.DateMode = view.DateModeSpecific; v.SpecificValue = "not-a-date" }, view.ErrInvalidSpecificValue},
		{"specific month value malformed", func(v *view.View) {
			v.DateMode = view.DateModeSpecific
			v.SpecificPeriod = view.SpecificPeriodMonth
			v.SpecificValue = "2026-09-15"
		}, view.ErrInvalidSpecificValue},
		{"custom start not a datetime", func(v *view.View) { v.DateMode = view.DateModeCustom; v.FromDate = "2026/09/15" }, view.ErrInvalidDate},
		{"custom end not a datetime", func(v *view.View) { v.DateMode = view.DateModeCustom; v.ToDate = "tomorrow" }, view.ErrInvalidDate},
		{"unknown date mode", func(v *view.View) { v.DateMode = "week" }, view.ErrInvalidDateMode},
		{"unknown date preset", func(v *view.View) { v.DatePreset = "tomorrow" }, view.ErrInvalidDatePreset},
		{"unknown specific period", func(v *view.View) { v.SpecificPeriod = "week" }, view.ErrInvalidSpecificPeriod},
		{"specific value too long", func(v *view.View) { v.SpecificValue = strings.Repeat("v", view.MaxSpecificValueLength+1) }, view.ErrSpecificValueTooLong},
		{"date too long", func(v *view.View) { v.FromDate = strings.Repeat("d", view.MaxDateLength+1) }, view.ErrDateTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &view.View{Name: "runs", Type: view.TypeRun}
			v.Normalize()
			tt.mutate(v)
			assert.ErrorIs(t, v.Validate(), tt.want)
		})
	}
}

func TestView_NormalizeRunDefaults(t *testing.T) {
	v := &view.View{Name: "runs", Type: view.TypeRun}
	v.Normalize()

	assert.Equal(t, view.WorkspaceScopeAll, v.WorkspaceScope)
	assert.Equal(t, view.RunStatusAll, v.RunStatus)
	assert.Equal(t, view.DateModePreset, v.DateMode)
	assert.Equal(t, view.DatePresetToday, v.DatePreset)
	assert.Equal(t, view.SpecificPeriodDate, v.SpecificPeriod)
	require.NoError(t, v.Validate())
}

func TestView_RunSpecificValues(t *testing.T) {
	okValues := []struct {
		period string
		value  string
	}{
		{view.SpecificPeriodDate, "2026-09-15"},
		{view.SpecificPeriodMonth, "2026-09"},
		{view.SpecificPeriodYear, "2026"},
	}
	for _, tt := range okValues {
		assert.True(t, view.ValidRunSpecificValue(tt.period, tt.value), tt)
	}

	badValues := []struct {
		period string
		value  string
	}{
		{view.SpecificPeriodDate, "2026-13-40"},
		{view.SpecificPeriodDate, "September 15"},
		{view.SpecificPeriodMonth, "2026-9"},
		{view.SpecificPeriodMonth, "2026-09-15"},
		{view.SpecificPeriodYear, "26"},
		{view.SpecificPeriodDate, ""},
	}
	for _, tt := range badValues {
		assert.False(t, view.ValidRunSpecificValue(tt.period, tt.value), tt)
	}
}

func TestView_RunDateStrings(t *testing.T) {
	assert.True(t, view.ValidRunDateString(""))
	assert.True(t, view.ValidRunDateString("2026-09-15T00:00"))
	assert.True(t, view.ValidRunDateString("2026-09-15T00:00:00"))
	assert.False(t, view.ValidRunDateString("2026-09-15"))
	assert.False(t, view.ValidRunDateString("tomorrow"))
}

func TestView_RunStatusBounds(t *testing.T) {
	for status := view.MinRunStatusCode; status <= view.MaxRunStatusCode; status++ {
		assert.True(t, view.ValidRunStatus(strconv.Itoa(status)), status)
	}
	assert.True(t, view.ValidRunStatus(view.RunStatusAll))
	assert.False(t, view.ValidRunStatus("9"))
	assert.False(t, view.ValidRunStatus("-1"))
	assert.False(t, view.ValidRunStatus("all-runs"))
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
		ActiveOnly:     true,
		Default:        true,
		Pinned:         true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	assert.Equal(t, original, original.ToStorage().ToView())
}

func TestView_RunStorageRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	original := &view.View{
		ID:             "run-id",
		Name:           "Failed runs",
		Type:           view.TypeRun,
		WorkspaceScope: view.WorkspaceScopeWorkspace,
		Workspace:      "production",
		DAGName:        "etl",
		Labels:         []string{"team=platform"},
		DAGRunID:       "019df6cf-0127-7340-bd96-d51bc1453045",
		RunStatus:      "5",
		DateMode:       view.DateModeSpecific,
		DatePreset:     view.DatePresetToday,
		SpecificPeriod: view.SpecificPeriodMonth,
		SpecificValue:  "2026-09",
		FromDate:       "2026-09-01T00:00",
		ToDate:         "2026-09-30T23:59",
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
