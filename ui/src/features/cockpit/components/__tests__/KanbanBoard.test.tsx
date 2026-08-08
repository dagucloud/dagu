// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { KanbanBoard } from '../KanbanBoard';
import type {
  KanbanColumnData,
  KanbanColumns,
} from '../../hooks/useDateKanbanData';

vi.mock('@/hooks/useIsMobile', () => ({
  useIsMobile: () => false,
}));

vi.mock('../KanbanColumn', () => ({
  KanbanColumn: ({ title }: { title: string }) => <div>{title}</div>,
}));

function column(): KanbanColumnData {
  return {
    runs: [],
    hasMore: false,
    isInitialLoading: false,
    isLoadingMore: false,
    error: null,
    loadMoreError: null,
    loadMore: vi.fn(async () => undefined),
    retry: vi.fn(async () => undefined),
  };
}

function emptyColumns(): KanbanColumns {
  return {
    queued: column(),
    running: column(),
    review: column(),
    done: column(),
    failed: column(),
  };
}

describe('KanbanBoard', () => {
  it.each([
    ['without a column selection', undefined],
    ['with an explicit empty selection', []],
  ] as const)(
    'keeps every default column visible %s',
    (_name, visibleColumns) => {
      render(
        <KanbanBoard
          columns={emptyColumns()}
          visibleColumns={visibleColumns}
          onCardClick={vi.fn()}
          onArtifactsClick={vi.fn()}
        />
      );

      for (const label of ['Queued', 'Running', 'Review', 'Done', 'Failed']) {
        expect(screen.getByText(label)).toBeInTheDocument();
      }
    }
  );
});
