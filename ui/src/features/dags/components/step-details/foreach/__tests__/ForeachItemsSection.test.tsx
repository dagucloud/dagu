// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  components,
  NodeStatus,
  NodeStatusLabel,
  Stream,
} from '@/api/v1/schema';
import { ForeachItemsSection } from '../ForeachItemsSection';

type ForeachItemList = components['schemas']['ForeachItemList'];
type ForeachItem = components['schemas']['ForeachItem'];

const api = vi.hoisted(() => ({
  list: null as ForeachItemList | null,
  items: {} as Record<string, ForeachItem>,
  logs: { content: 'real cause for b', lineCount: 1, totalLines: 1 },
  calls: [] as Array<{ path: string; query: Record<string, unknown> }>,
}));

vi.mock('@/contexts/RemoteNodeContext', () => ({ useRemoteNode: () => 'local' }));
vi.mock('@/hooks/api', () => ({
  useQuery: (
    path: string,
    init: { params: { query: Record<string, unknown>; path: { item?: string } } } | null
  ) => {
    if (!init) {
      return { data: undefined, isLoading: false };
    }
    api.calls.push({ path, query: init.params.query });
    if (path.endsWith('/foreach')) {
      return { data: api.list ?? undefined, isLoading: !api.list };
    }
    if (path.endsWith('/foreach/{item}')) {
      return { data: api.items[init.params.path.item ?? ''], isLoading: false };
    }
    return { data: api.logs, isLoading: false };
  },
}));

const stepRef = {
  dagName: 'example',
  dagRunId: 'run-1',
  stepName: 'each',
};

const failedItem: ForeachItem = {
  item: '1',
  index: 1,
  key: 'b',
  status: NodeStatus.Failed,
  statusLabel: NodeStatusLabel.failed,
  error: 'exit status 3',
  startedAt: '2026-10-07T00:00:00Z',
  finishedAt: '2026-10-07T00:00:02Z',
  steps: [
    {
      name: 'body',
      status: NodeStatus.Failed,
      statusLabel: NodeStatusLabel.failed,
      error: 'exit status 3',
      hasStdout: true,
      hasStderr: true,
    },
    {
      name: 'inner',
      status: NodeStatus.Success,
      statusLabel: NodeStatusLabel.succeeded,
      startedAt: '2026-10-07T00:00:02Z',
      finishedAt: '2026-10-07T00:00:03Z',
      hasStdout: false,
      hasStderr: false,
      foreachParent: '1.inner',
    },
    {
      name: 'skipped_inner',
      status: NodeStatus.Aborted,
      statusLabel: NodeStatusLabel.aborted,
      error: 'upstream failed',
      hasStdout: false,
      hasStderr: false,
      foreachParent: '1.skipped_inner',
    },
  ],
};

beforeEach(() => {
  api.calls = [];
  api.items = { '1': failedItem };
  api.list = {
    total: 3,
    counts: { notStarted: 1, running: 0, succeeded: 1, failed: 1, aborted: 0 },
    items: [
      {
        item: '1',
        index: 1,
        key: 'b',
        status: NodeStatus.Failed,
        statusLabel: NodeStatusLabel.failed,
        error: 'exit status 3',
        startedAt: '2026-10-07T00:00:00Z',
        finishedAt: '2026-10-07T00:00:02Z',
      },
      {
        item: '0',
        index: 0,
        key: 'a',
        status: NodeStatus.Success,
        statusLabel: NodeStatusLabel.succeeded,
      },
      {
        item: '2',
        index: 2,
        key: '2',
        status: NodeStatus.NotStarted,
        statusLabel: NodeStatusLabel.not_started,
      },
    ],
  };
});

describe('ForeachItemsSection', () => {
  it('lists items failed first with their counts', () => {
    render(
      <ForeachItemsSection
        stepRef={stepRef}
        stepStatus={NodeStatus.PartialSuccess}
      />
    );

    expect(screen.getByText('2 of 3 done')).toBeVisible();
    const rows = screen.getAllByRole('button', { name: /^Item \d+$/ });
    expect(rows.map((row) => row.getAttribute('aria-label'))).toEqual([
      'Item 1',
      'Item 0',
      'Item 2',
    ]);
    expect(rows[0]).toHaveTextContent('exit status 3');
    expect(rows[0]).toHaveTextContent('2s');
    expect(screen.getByRole('button', { name: 'Failed 1' })).toBeVisible();
    // A finished step does not poll.
    expect(api.calls[0]?.query).toMatchObject({ page: 1, perPage: 50 });
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('filters by status through the API', () => {
    render(
      <ForeachItemsSection
        stepRef={stepRef}
        stepStatus={NodeStatus.PartialSuccess}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: 'Succeeded 1' }));
    expect(api.calls[api.calls.length - 1]?.query).toMatchObject({ status: 'succeeded' });
  });

  it('expands an item into body steps, inline logs, and nested items', () => {
    const onOpenLog = vi.fn();
    render(
      <ForeachItemsSection
        stepRef={stepRef}
        stepStatus={NodeStatus.PartialSuccess}
        onOpenLog={onOpenLog}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Item 1' }));
    const body = screen.getByText('body').closest('div')!.parentElement!;
    expect(body).toHaveTextContent('failed');

    fireEvent.click(within(body).getByRole('button', { name: 'stderr' }));
    expect(screen.getByText('real cause for b')).toBeVisible();
    expect(api.calls[api.calls.length - 1]).toMatchObject({
      path: '/dag-runs/{name}/{dagRunId}/steps/{stepName}/foreach/{item}/steps/{bodyStepName}/log',
      query: { stream: 'stderr', tail: 100 },
    });

    fireEvent.click(
      within(body).getByRole('button', { name: 'Open body in log viewer' })
    );
    expect(onOpenLog).toHaveBeenCalledWith(
      'body',
      { stepName: 'each', item: '1' },
      Stream.stderr,
      NodeStatus.Failed
    );

    // A nested foreach body step that ran lists its own items by parent; one
    // that never started lists nothing.
    expect(
      api.calls.some(
        (call) => call.path.endsWith('/foreach') && call.query.parent === '1.inner'
      )
    ).toBe(true);
    expect(
      api.calls.some((call) => call.query.parent === '1.skipped_inner')
    ).toBe(false);
  });

  it('shows a waiting state while a running step has no items yet', () => {
    api.list = {
      total: 0,
      counts: { notStarted: 0, running: 0, succeeded: 0, failed: 0, aborted: 0 },
      items: [],
    };
    render(
      <ForeachItemsSection stepRef={stepRef} stepStatus={NodeStatus.Running} />
    );
    expect(screen.getByText('Waiting for items…')).toBeVisible();
    expect(api.calls[0]?.query).toMatchObject({ page: 1 });
  });

  it('explains missing records for a finished step', () => {
    api.list = {
      total: 0,
      counts: { notStarted: 0, running: 0, succeeded: 0, failed: 0, aborted: 0 },
      items: [],
    };
    render(
      <ForeachItemsSection stepRef={stepRef} stepStatus={NodeStatus.Success} />
    );
    expect(screen.getByText('No item records for this run.')).toBeVisible();
  });
});
