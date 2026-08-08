// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package pagination

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaginator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		page       int
		perPage    int
		wantLimit  int
		wantOffset int
	}{
		{name: "Defaults", wantLimit: defaultPerPage},
		{name: "SecondPage", page: 2, perPage: 10, wantLimit: 10, wantOffset: 10},
		{name: "NegativePage", page: -1, perPage: 20, wantLimit: 20},
		{name: "Maximum", page: 1, perPage: 300, wantLimit: maxPerPage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NewPaginator(tt.page, tt.perPage)
			require.Equal(t, tt.wantLimit, got.Limit())
			require.Equal(t, tt.wantOffset, got.Offset())
		})
	}
}

func TestPaginatedResult(t *testing.T) {
	t.Parallel()

	result := NewPaginatedResult([]int{4, 5, 6}, 8, NewPaginator(2, 3))
	require.Equal(t, []int{4, 5, 6}, result.Data())
	require.Equal(t, 2, result.CurrentPage)
	require.Equal(t, 3, result.TotalPages)
	require.Equal(t, 8, result.TotalCount)
	require.Equal(t, 3, result.Offset)
	require.True(t, result.HasNextPage)
	require.True(t, result.HasPrevPage)
	require.Equal(t, 3, result.NextPage)
	require.Equal(t, 1, result.PrevPage)

	empty := NewPaginatedResult[int](nil, 0, Paginator{})
	require.Empty(t, empty.Items)
	require.Equal(t, 1, empty.TotalPages)
}
