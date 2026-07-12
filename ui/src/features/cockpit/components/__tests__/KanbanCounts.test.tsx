// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel, TriggerType, ViewColumn } from '@/api/v1/schema';
import { KanbanColumn } from '../KanbanColumn';
import { KanbanBoard } from '../KanbanBoard';
import { MobileKanbanBoard } from '../MobileKanbanBoard';
import type {
  KanbanColumnData,
  KanbanColumns,
} from '../../hooks/useDateKanbanData';

vi.mock('framer-motion', () => ({
  AnimatePresence: ({ children }: { children: React.ReactNode }) => (
    <>{children}</>
  ),
  LayoutGroup: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  motion: {
    div: ({
      children,
      ...props
    }: React.ComponentProps<'div'> & Record<string, unknown>) => {
      const divProps = { ...props };
      delete divProps.layoutId;
      delete divProps.layout;
      delete divProps.initial;
      delete divProps.animate;
      delete divProps.exit;
      delete divProps.transition;
      return <div {...divProps}>{children}</div>;
    },
  },
}));

vi.mock('../KanbanCard', () => ({
  KanbanCard: ({ run }: { run: { dagRunId: string } }) => (
    <div data-testid={`kanban-card-${run.dagRunId}`} />
  ),
}));

vi.mock('@/hooks/useIsMobile', () => ({
  useIsMobile: () => false,
}));

function createColumn(
  overrides: Partial<KanbanColumnData> = {}
): KanbanColumnData {
  return {
    runs: [
      {
        dagRunId: 'run-1',
        name: 'example',
        status: Status.Running,
        statusLabel: StatusLabel.running,
        artifactsAvailable: false,
        autoRetryCount: 0,
        triggerType: TriggerType.manual,
        queuedAt: '',
        scheduleTime: '',
        startedAt: '',
        finishedAt: '',
      },
      {
        dagRunId: 'run-2',
        name: 'example',
        status: Status.Running,
        statusLabel: StatusLabel.running,
        artifactsAvailable: false,
        autoRetryCount: 0,
        triggerType: TriggerType.manual,
        queuedAt: '',
        scheduleTime: '',
        startedAt: '',
        finishedAt: '',
      },
    ],
    hasMore: false,
    isInitialLoading: false,
    isLoadingMore: false,
    error: null,
    loadMoreError: null,
    loadMore: vi.fn(async () => {}),
    retry: vi.fn(async () => {}),
    ...overrides,
  };
}

beforeEach(() => {
  localStorage.clear();
});

describe('cockpit count labels', () => {
  it('shows a plus in desktop headers when the column has more runs', () => {
    render(
      <KanbanColumn
        title="Running"
        column={createColumn({ hasMore: true })}
        onCardClick={() => {}}
        onArtifactsClick={() => {}}
      />
    );

    expect(screen.getByText('2+')).toBeInTheDocument();
  });

  it('shows a plus in mobile tabs when the column has more runs', () => {
    const columns: KanbanColumns = {
      queued: createColumn(),
      running: createColumn({ hasMore: true }),
      review: createColumn({ runs: [] }),
      done: createColumn({ runs: [] }),
      failed: createColumn({ runs: [] }),
    };

    render(
      <MobileKanbanBoard
        columns={columns}
        onCardClick={() => {}}
        onArtifactsClick={() => {}}
      />
    );

    expect(screen.getByRole('tab', { name: /Running/ })).toHaveTextContent(
      'Running2+'
    );
  });

  it('bounds the mobile column area so the active tab can scroll internally', () => {
    const columns: KanbanColumns = {
      queued: createColumn(),
      running: createColumn({ hasMore: true }),
      review: createColumn({ runs: [] }),
      done: createColumn({ runs: [] }),
      failed: createColumn({ runs: [] }),
    };

    const { container } = render(
      <MobileKanbanBoard
        columns={columns}
        onCardClick={() => {}}
        onArtifactsClick={() => {}}
      />
    );

    const boardRoot = container.firstElementChild;
    expect(boardRoot).not.toHaveClass('max-h-[70vh]');
    expect(boardRoot?.firstElementChild).toHaveClass('overflow-y-hidden');
    expect(boardRoot?.lastElementChild).toHaveClass('max-h-[70vh]');
    expect(boardRoot?.lastElementChild).toHaveClass('overflow-hidden');
  });

  it('renders only configured desktop columns in their saved order', () => {
    const columns: KanbanColumns = {
      queued: createColumn(),
      running: createColumn(),
      review: createColumn({ runs: [] }),
      done: createColumn({ runs: [] }),
      failed: createColumn({ runs: [] }),
    };

    render(
      <KanbanBoard
        columns={columns}
        visibleColumns={[ViewColumn.failed, ViewColumn.running]}
        onCardClick={() => {}}
        onArtifactsClick={() => {}}
      />
    );

    const labels = screen
      .getAllByRole('region')
      .map((region) => region.getAttribute('aria-label'));
    expect(labels).toEqual(['Failed column', 'Running column']);
  });

  it('renders only configured mobile tabs and falls back to a visible tab', () => {
    localStorage.setItem('dagu_cockpit_active_tab', ViewColumn.queued);
    const columns: KanbanColumns = {
      queued: createColumn(),
      running: createColumn(),
      review: createColumn({ runs: [] }),
      done: createColumn({ runs: [] }),
      failed: createColumn({ runs: [] }),
    };

    render(
      <MobileKanbanBoard
        columns={columns}
        visibleColumns={[ViewColumn.failed, ViewColumn.running]}
        onCardClick={() => {}}
        onArtifactsClick={() => {}}
      />
    );

    expect(screen.getAllByRole('tab')).toHaveLength(2);
    expect(screen.getByRole('tab', { name: /Failed/ })).toBeInTheDocument();
    expect(
      screen.queryByRole('tab', { name: /Queued/ })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('tab', { name: /Running/ })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByRole('tabpanel')).toHaveAccessibleName(/Running/);
  });

  it('supports keyboard navigation between mobile tabs', async () => {
    const user = userEvent.setup();
    const columns: KanbanColumns = {
      queued: createColumn(),
      running: createColumn(),
      review: createColumn({ runs: [] }),
      done: createColumn({ runs: [] }),
      failed: createColumn({ runs: [] }),
    };

    render(
      <MobileKanbanBoard
        columns={columns}
        visibleColumns={[ViewColumn.failed, ViewColumn.running]}
        onCardClick={() => {}}
        onArtifactsClick={() => {}}
      />
    );

    const runningTab = screen.getByRole('tab', { name: /Running/ });
    runningTab.focus();
    await user.keyboard('{ArrowRight}');

    const failedTab = screen.getByRole('tab', { name: /Failed/ });
    expect(failedTab).toHaveFocus();
    expect(failedTab).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tabpanel')).toHaveAccessibleName(/Failed/);
  });
});
