// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package view_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/view"
)

func validView() *view.View {
	return &view.View{
		ID:           "id-1",
		Name:         "My View",
		Type:         view.TypeKanban,
		LookbackDays: 3,
	}
}

func TestView_Validate_OK(t *testing.T) {
	require.NoError(t, validView().Validate())
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
		{"lookback zero", func(v *view.View) { v.LookbackDays = 0 }, view.ErrInvalidLookback},
		{"lookback too large", func(v *view.View) { v.LookbackDays = view.MaxLookbackDays + 1 }, view.ErrInvalidLookback},
		{"too many labels", func(v *view.View) { v.Labels = make([]string, view.MaxLabels+1) }, view.ErrTooManyLabels},
		{"unknown type", func(v *view.View) { v.Type = "timeline" }, view.ErrInvalidType},
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
		LookbackDays: 0,
	}
	v.Normalize()

	assert.Equal(t, "spaced", v.Name)
	assert.Equal(t, view.TypeKanban, v.Type, "empty type defaults to kanban")
	assert.Equal(t, "ws", v.Workspace)
	assert.Equal(t, "dag", v.DAGName)
	assert.Equal(t, []string{"a", "b"}, v.Labels, "empty and oversized labels are dropped")
	assert.Equal(t, view.DefaultLookbackDays, v.LookbackDays, "zero lookback defaults")
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
		LookbackDays: 7,
		Pinned:       true,
		CreatedBy:    "alice",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	got := original.ToStorage().ToView()
	assert.Equal(t, original, got)
}
