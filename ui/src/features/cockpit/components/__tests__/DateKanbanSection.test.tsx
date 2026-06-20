// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, render } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DateKanbanSection } from '../DateKanbanSection';
import { useDateKanbanData } from '../../hooks/useDateKanbanData';

vi.mock('../../hooks/useDateKanbanData', () => ({
  useDateKanbanData: vi.fn(),
}));

vi.mock('../KanbanBoard', () => ({
  KanbanBoard: () => <div>board</div>,
}));

const useDateKanbanDataMock = vi.mocked(useDateKanbanData);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const defaultReturn = {
  columns: {
    queued: {
      runs: [],
      hasMore: false,
      isInitialLoading: false,
      isLoadingMore: false,
      error: null,
      loadMoreError: null,
      loadMore: vi.fn(async () => {}),
      retry: vi.fn(async () => {}),
    },
    running: {
      runs: [],
      hasMore: false,
      isInitialLoading: false,
      isLoadingMore: false,
      error: null,
      loadMoreError: null,
      loadMore: vi.fn(async () => {}),
      retry: vi.fn(async () => {}),
    },
    review: {
      runs: [],
      hasMore: false,
      isInitialLoading: false,
      isLoadingMore: false,
      error: null,
      loadMoreError: null,
      loadMore: vi.fn(async () => {}),
      retry: vi.fn(async () => {}),
    },
    done: {
      runs: [],
      hasMore: false,
      isInitialLoading: false,
      isLoadingMore: false,
      error: null,
      loadMoreError: null,
      loadMore: vi.fn(async () => {}),
      retry: vi.fn(async () => {}),
    },
    failed: {
      runs: [],
      hasMore: false,
      isInitialLoading: false,
      isLoadingMore: false,
      error: null,
      loadMoreError: null,
      loadMore: vi.fn(async () => {}),
      retry: vi.fn(async () => {}),
    },
  },
  error: null,
  isLoading: false,
  isEmpty: true,
  retry: vi.fn(async () => {}),
};

describe('DateKanbanSection live-update flags', () => {
  it('passes isToday=true and isLive=true for today', () => {
    useDateKanbanDataMock.mockReturnValue(defaultReturn);

    render(
      <DateKanbanSection
        date="2026-03-22"
        todayStr="2026-03-22"
        onCardClick={() => {}}
        onArtifactsClick={() => {}}
      />
    );

    expect(useDateKanbanDataMock).toHaveBeenCalledWith(
      '2026-03-22',
      true,
      true,
      undefined
    );
  });

  it('passes isToday=false and isLive=true for yesterday', () => {
    useDateKanbanDataMock.mockReturnValue(defaultReturn);

    render(
      <DateKanbanSection
        date="2026-03-21"
        todayStr="2026-03-22"
        onCardClick={() => {}}
        onArtifactsClick={() => {}}
      />
    );

    expect(useDateKanbanDataMock).toHaveBeenCalledWith(
      '2026-03-21',
      false,
      true,
      undefined
    );
  });

  it('passes isToday=false and isLive=false for older dates', () => {
    useDateKanbanDataMock.mockReturnValue(defaultReturn);

    render(
      <DateKanbanSection
        date="2026-03-20"
        todayStr="2026-03-22"
        onCardClick={() => {}}
        onArtifactsClick={() => {}}
      />
    );

    expect(useDateKanbanDataMock).toHaveBeenCalledWith(
      '2026-03-20',
      false,
      false,
      undefined
    );
  });
});
