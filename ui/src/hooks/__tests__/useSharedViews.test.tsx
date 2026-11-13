// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ViewSpecType } from '@/api/v1/schema';
import type { View, ViewSpec } from '@/hooks/useViews';
import { useSharedViews } from '../useSharedViews';

const stores = vi.hoisted(() => ({
  workflow: {} as Record<string, unknown>,
  run: {} as Record<string, unknown>,
}));

vi.mock('@/hooks/useViews', () => ({
  useViews: (type: string) => (type === 'run' ? stores.run : stores.workflow),
}));

function makeView(
  id: string,
  name: string,
  type: ViewSpecType,
  extra: Partial<View> = {}
): View {
  return {
    id,
    name,
    type,
    intervalDays: 1,
    createdAt: '',
    updatedAt: '',
    ...extra,
  } as View;
}

function makeStore(views: View[]) {
  return {
    views,
    isLoading: false,
    error: null,
    createView: vi.fn().mockResolvedValue({ id: 'created' }),
    updateView: vi.fn().mockResolvedValue({ id: 'updated' }),
    deleteView: vi.fn().mockResolvedValue(undefined),
  };
}

const runFields = {
  dagRunId: 'etl*',
  runStatus: 'failed',
  dateMode: 'preset',
  datePreset: 'last7days',
  specificPeriod: 'day',
  specificValue: '2026-03-13',
  fromDate: '2026-03-01T00:00:00Z',
  toDate: '2026-03-13T00:00:00Z',
} as unknown as Partial<View>;

const workflowFields = {
  activeOnly: true,
  sortField: 'name',
  sortOrder: 'desc',
} as unknown as Partial<View>;

beforeEach(() => {
  stores.workflow = makeStore([
    makeView('w-b', 'Beta', ViewSpecType.workflow, workflowFields),
    makeView('w-d', 'delta', ViewSpecType.workflow),
  ]);
  stores.run = makeStore([
    makeView('r-a', 'Alpha', ViewSpecType.run, runFields),
    makeView('r-c', 'Charlie', ViewSpecType.run),
  ]);
});

function store(kind: 'workflow' | 'run') {
  return stores[kind] as unknown as ReturnType<typeof makeStore>;
}

describe('useSharedViews', () => {
  it('merges the workflow and run views sorted by name', () => {
    const { result } = renderHook(() => useSharedViews('workflow'));

    expect(result.current.views.map((view) => view.id)).toEqual([
      'r-a',
      'w-b',
      'r-c',
      'w-d',
    ]);
  });

  it('reports loading and the first error of either store', () => {
    store('run').isLoading = true;
    (store('run') as { error: unknown }).error = new Error('run failed');
    const { result } = renderHook(() => useSharedViews('run'));

    expect(result.current.isLoading).toBe(true);
    expect(result.current.error).toEqual(new Error('run failed'));
  });

  it.each(['workflow', 'run'] as const)(
    'creates a workflow view from the %s mode',
    async (mode) => {
      const { result } = renderHook(() => useSharedViews(mode));
      const spec = {
        name: 'New',
        type: ViewSpecType.run,
        intervalDays: 1,
        pinned: false,
      } as ViewSpec;

      await result.current.createView(spec);

      expect(store('workflow').createView).toHaveBeenCalledWith({
        ...spec,
        type: ViewSpecType.workflow,
      });
      expect(store('run').createView).not.toHaveBeenCalled();
    }
  );

  it('keeps the run fields of a workflow view saved from the workflow mode', async () => {
    const { result } = renderHook(() => useSharedViews('workflow'));
    const spec = {
      name: 'Alpha',
      type: ViewSpecType.workflow,
      intervalDays: 1,
      pinned: false,
      activeOnly: false,
      sortField: 'status',
      sortOrder: 'asc',
      dagRunId: 'ignored',
      runStatus: 'success',
    } as unknown as ViewSpec;

    await result.current.updateView('r-a', spec);

    // The view is a run view: it keeps its type and its run fields.
    expect(store('workflow').updateView).not.toHaveBeenCalled();
    expect(store('run').updateView).toHaveBeenCalledWith('r-a', {
      ...spec,
      ...runFields,
      type: ViewSpecType.run,
    });
    const sent = store('run').updateView.mock.calls[0]?.[1];
    expect(sent?.activeOnly).toBe(false);
    expect(sent?.sortField).toBe('status');
  });

  it('keeps the workflow fields of a view saved from the run mode', async () => {
    const { result } = renderHook(() => useSharedViews('run'));
    const spec = {
      name: 'Beta',
      type: ViewSpecType.run,
      intervalDays: 1,
      pinned: false,
      dagRunId: 'new*',
      runStatus: 'success',
      activeOnly: false,
      sortField: 'status',
      sortOrder: 'asc',
    } as unknown as ViewSpec;

    await result.current.updateView('w-b', spec);

    // The view is a workflow view: the update keeps its type and its fields.
    expect(store('run').updateView).not.toHaveBeenCalled();
    expect(store('workflow').updateView).toHaveBeenCalledWith('w-b', {
      ...spec,
      ...workflowFields,
      type: ViewSpecType.workflow,
    });
    const sent = store('workflow').updateView.mock.calls[0]?.[1];
    expect(sent?.dagRunId).toBe('new*');
    expect(sent?.runStatus).toBe('success');
  });

  it('updates an unknown view through the workflow store', async () => {
    const { result } = renderHook(() => useSharedViews('run'));
    const spec = {
      name: 'Gone',
      type: ViewSpecType.run,
      intervalDays: 1,
      pinned: false,
    } as ViewSpec;

    await result.current.updateView('missing', spec);

    expect(store('workflow').updateView).toHaveBeenCalledWith('missing', spec);
    expect(store('run').updateView).not.toHaveBeenCalled();
  });

  it('deletes a view of either type', async () => {
    const { result } = renderHook(() => useSharedViews('workflow'));

    await result.current.deleteView('r-a');

    expect(store('workflow').deleteView).toHaveBeenCalledWith('r-a');
  });
});
