// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

const (
	defaultPerPage = 50
	minPage        = 1
	maxPerPage     = 200
)

type Paginator struct {
	limit       int
	offset      int
	currentPage int
	perPage     int
	initialized bool
}

func NewPaginator(page, perPage int) Paginator {
	page = max(page, 1)
	maxInt := int(^uint(0) >> 1)
	if perPage > maxPerPage && perPage != maxInt {
		perPage = maxPerPage
	}
	if perPage == 0 {
		perPage = defaultPerPage
	}
	limit := perPage
	offset := (page - 1) * perPage
	return Paginator{
		limit:       limit,
		offset:      offset,
		currentPage: page,
		perPage:     perPage,
		initialized: true,
	}
}

func DefaultPaginator() Paginator {
	return NewPaginator(minPage, defaultPerPage)
}

func (pg *Paginator) Limit() int {
	return pg.limit
}

func (pg *Paginator) Offset() int {
	return pg.offset
}

type PaginatedResult[T any] struct {
	Items       []T
	CurrentPage int
	TotalPages  int
	TotalCount  int
	Offset      int
	HasNextPage bool
	HasPrevPage bool
	NextPage    int
	PrevPage    int
}

func NewPaginatedResult[T any](items []T, total int, pg Paginator) PaginatedResult[T] {
	if items == nil {
		items = make([]T, 0)
	}
	if !pg.initialized {
		pg = DefaultPaginator()
	}
	totalPages := (total-1)/(pg.perPage) + 1

	nextPage := min(pg.currentPage+1, totalPages)
	prevPage := max(pg.currentPage-1, 1)

	return PaginatedResult[T]{
		Items:       items,
		CurrentPage: pg.currentPage,
		TotalPages:  totalPages,
		TotalCount:  total,
		Offset:      pg.offset,
		HasNextPage: pg.currentPage < totalPages,
		HasPrevPage: pg.currentPage > 1,
		NextPage:    nextPage,
		PrevPage:    prevPage,
	}
}

func (r PaginatedResult[T]) Data() []T {
	return r.Items
}
