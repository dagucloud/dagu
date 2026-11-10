// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import { useRangeKanbanData } from '../useDateKanbanData';

const usePaginatedDAGRunsMock = vi.hoisted(() => vi.fn());

vi.mock('@/features/dag-runs/hooks/dagRunPagination', () => ({
  usePaginatedDAGRuns: usePaginatedDAGRunsMock,
}));

function queries(): Record<string, unknown>[] {
  return usePaginatedDAGRunsMock.mock.calls.map(([options]) => options.query);
}

function isReview(query: Record<string, unknown>): boolean {
  return (query.status as Status[]).includes(Status.Waiting);
}

describe('useRangeKanbanData', () => {
  beforeEach(() => {
    usePaginatedDAGRunsMock.mockReset();
    usePaginatedDAGRunsMock.mockReturnValue({
      dagRuns: [],
      error: undefined,
      isInitialLoading: false,
      isLoadingMore: false,
      loadMoreError: undefined,
      hasMore: false,
      refresh: vi.fn(),
      loadMore: vi.fn(),
    });
  });

  it('leaves the runs of service DAGs out of the boards', () => {
    renderHook(() => useRangeKanbanData(0, 86400, false, 5000));

    const boards = queries().filter((query) => !isReview(query));
    expect(boards.length).toBeGreaterThan(0);
    for (const query of boards) {
      expect(query.excludeServiceDAGRuns).toBe(true);
    }
  });

  // A run that waits for a person needs one, whatever DAG it belongs to.
  it('keeps the runs of service DAGs in the review column', () => {
    renderHook(() => useRangeKanbanData(0, 86400, false, 5000));

    const review = queries().filter(isReview);
    expect(review.length).toBeGreaterThan(0);
    for (const query of review) {
      expect(query.excludeServiceDAGRuns).toBeUndefined();
    }
  });

  // A search by name must find a service DAG's runs too.
  it('keeps the runs of service DAGs in a search by name', () => {
    renderHook(() =>
      useRangeKanbanData(0, 86400, false, 5000, { name: 'worker' })
    );

    expect(queries().length).toBeGreaterThan(0);
    for (const query of queries()) {
      expect(query.name).toBe('worker');
      expect(query.excludeServiceDAGRuns).toBeUndefined();
    }
  });
});
