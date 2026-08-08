// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel } from '@/api/v1/schema';
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

function column(runs: KanbanColumnData['runs'] = []): KanbanColumnData {
  return {
    runs,
    hasMore: false,
    isInitialLoading: false,
    isLoadingMore: false,
    error: null,
    loadMoreError: null,
    loadMore: vi.fn(async () => undefined),
    retry: vi.fn(async () => undefined),
  };
}

describe('KanbanBoard', () => {
  it('omits empty desktop columns when another column has runs', () => {
    const columns: KanbanColumns = {
      queued: column(),
      running: column([
        {
          name: 'deploy',
          dagRunId: 'run-1',
          status: Status.Running,
          statusLabel: StatusLabel.running,
        } as KanbanColumnData['runs'][number],
      ]),
      review: column(),
      done: column(),
      failed: column(),
    };

    render(
      <KanbanBoard
        columns={columns}
        onCardClick={vi.fn()}
        onArtifactsClick={vi.fn()}
      />
    );

    expect(screen.getByText('Running')).toBeInTheDocument();
    expect(screen.queryByText('Queued')).not.toBeInTheDocument();
    expect(screen.queryByText('Review')).not.toBeInTheDocument();
    expect(screen.queryByText('Done')).not.toBeInTheDocument();
    expect(screen.queryByText('Failed')).not.toBeInTheDocument();
  });
});
