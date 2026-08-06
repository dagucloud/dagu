// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"reflect"
	"testing"
)

func TestNewPaginator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		page        int
		perPage     int
		wantLimit   int
		wantOffset  int
		wantCurPage int
		wantPerPage int
	}{
		{"Default values", 0, 0, defaultPerPage, 0, 1, defaultPerPage},
		{"Normal case", 2, 10, 10, 10, 2, 10},
		{"Negative page", -1, 20, 20, 0, 1, 20},
		{"Exceed max per page", 1, 300, maxPerPage, 0, 1, maxPerPage},
		{"Large page number", 1000, 10, 10, 9990, 1000, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := NewPaginator(tt.page, tt.perPage)
			if got.Limit() != tt.wantLimit {
				t.Errorf("Limit = %v, want %v", got.Limit(), tt.wantLimit)
			}
			if got.Offset() != tt.wantOffset {
				t.Errorf("Offset = %v, want %v", got.Offset(), tt.wantOffset)
			}
			if got.currentPage != tt.wantCurPage {
				t.Errorf("CurrentPage = %v, want %v", got.currentPage, tt.wantCurPage)
			}
			if got.perPage != tt.wantPerPage {
				t.Errorf("PerPage = %v, want %v", got.perPage, tt.wantPerPage)
			}
		})
	}
}

func TestNewResult(t *testing.T) {
	t.Parallel()

	type testItem struct{ ID int }
	tests := []struct {
		name      string
		items     []testItem
		total     int
		paginator Paginator
		want      PaginatedResult[testItem]
	}{
		{
			name:      "FirstPage",
			items:     []testItem{{1}, {2}, {3}},
			total:     10,
			paginator: NewPaginator(1, 3),
			want: PaginatedResult[testItem]{
				Items:       []testItem{{1}, {2}, {3}},
				CurrentPage: 1,
				TotalPages:  4,
				TotalCount:  10,
				Offset:      0,
				HasNextPage: true,
				HasPrevPage: false,
				NextPage:    2,
				PrevPage:    1,
			},
		},
		{
			name:      "LastPage",
			items:     []testItem{{9}, {10}},
			total:     10,
			paginator: NewPaginator(4, 3),
			want: PaginatedResult[testItem]{
				Items:       []testItem{{9}, {10}},
				CurrentPage: 4,
				TotalPages:  4,
				TotalCount:  10,
				Offset:      9,
				HasNextPage: false,
				HasPrevPage: true,
				NextPage:    4,
				PrevPage:    3,
			},
		},
		{
			name:      "EmptyResult",
			items:     []testItem{},
			total:     0,
			paginator: NewPaginator(1, 10),
			want: PaginatedResult[testItem]{
				Items:       []testItem{},
				CurrentPage: 1,
				TotalPages:  1,
				TotalCount:  0,
				Offset:      0,
				HasNextPage: false,
				HasPrevPage: false,
				NextPage:    1,
				PrevPage:    1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := NewPaginatedResult(tt.items, tt.total, tt.paginator)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewResult() = %v, want %v", got, tt.want)
			}
		})
	}
}
