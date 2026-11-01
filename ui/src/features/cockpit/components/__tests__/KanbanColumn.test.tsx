// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel, TriggerType } from '@/api/v1/schema';
import type { KanbanColumnData } from '../../hooks/useDateKanbanData';
import { KanbanColumn } from '../KanbanColumn';

const run = {
  dagRunId: 'run-1',
  name: 'nightly',
  status: Status.Success,
  statusLabel: StatusLabel.succeeded,
  artifactsAvailable: false,
  autoRetryCount: 0,
  triggerType: TriggerType.manual,
  queuedAt: '',
  scheduleTime: '',
  startedAt: '2026-03-16T00:00:00Z',
  finishedAt: '2026-03-16T00:01:00Z',
};

function column(): KanbanColumnData {
  return {
    runs: [run],
    hasMore: false,
    isInitialLoading: false,
    isLoadingMore: false,
    error: null,
    loadMoreError: null,
    loadMore: vi.fn(async () => undefined),
    retry: vi.fn(async () => undefined),
  };
}

describe('KanbanColumn', () => {
  // The click event carries Ctrl/Cmd, which opens the run in a new tab.
  it('passes the click event of a card with its run', () => {
    const onCardClick = vi.fn();
    render(
      <KanbanColumn
        title="Done"
        column={column()}
        onCardClick={onCardClick}
        onArtifactsClick={vi.fn()}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: /nightly/ }), {
      metaKey: true,
    });

    expect(onCardClick).toHaveBeenCalledWith(
      run,
      expect.objectContaining({ metaKey: true })
    );
  });
});
