// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Stream } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import ForeachBodyStepLog from '../ForeachBodyStepLog';

type Options = { refreshInterval?: number; dedupingInterval?: number };

// Responses keep their identity across renders, as SWR's do; the viewer
// caches the latest log, so a fresh object per render would never settle.
const api = vi.hoisted(() => {
  const item = (status: number) => ({
    item: '0',
    index: 0,
    key: 'a',
    status,
    statusLabel: 'running',
    steps: [
      {
        name: 'body',
        status,
        statusLabel: 'running',
        hasStdout: true,
        hasStderr: false,
      },
    ],
  });
  return {
    items: { running: item(1), succeeded: item(4), failed: item(2) },
    bodyStatus: 'running' as 'running' | 'succeeded' | 'failed',
    log: { data: { content: 'item a line 1', lineCount: 1, totalLines: 1 } },
    logOptions: [] as Options[],
    itemOptions: [] as Options[],
  };
});

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ apiURL: '/api/v1' }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/hooks/useStepLogSSE', () => ({
  useStepLogSSE: () => ({
    data: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: true,
    error: null,
  }),
}));
vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown, options: Options) => {
    if (!init) {
      return { data: undefined, isLoading: false, mutate: vi.fn() };
    }
    if (path.endsWith('/foreach/{item}')) {
      api.itemOptions.push(options);
      return { data: api.items[api.bodyStatus], isLoading: false };
    }
    api.logOptions.push(options);
    return {
      data: api.log.data,
      isLoading: false,
      mutate: () => Promise.resolve(undefined),
    };
  },
}));

const props = {
  dagName: 'example',
  dagRunId: 'run-1',
  bodyStepName: 'body',
  foreach: { stepName: 'each', item: '0' },
  stream: Stream.stdout,
};

beforeEach(() => {
  api.bodyStatus = 'running';
  api.logOptions = [];
  api.itemOptions = [];
});

// No run node describes a body step, so the full viewer follows its status
// through the item record: live while it runs, still once it finishes.
describe('ForeachBodyStepLog', () => {
  it('polls the log every two seconds while the body step runs', () => {
    render(
      <ForeachBodyStepLog {...props} initialStatus={NodeStatus.Running} />,
      { wrapper: UserPreferencesProvider }
    );

    expect(screen.getByText('item a line 1')).toBeVisible();
    expect(api.logOptions[api.logOptions.length - 1]).toMatchObject({
      refreshInterval: 2000,
      dedupingInterval: 1000,
    });
    expect(api.itemOptions[api.itemOptions.length - 1]).toMatchObject({
      refreshInterval: 2000,
      dedupingInterval: 1000,
    });
  });

  it('stops polling once the item record reports the body step finished', () => {
    const view = render(
      <ForeachBodyStepLog {...props} initialStatus={NodeStatus.Running} />,
      { wrapper: UserPreferencesProvider }
    );
    api.bodyStatus = 'succeeded';
    view.rerender(
      <ForeachBodyStepLog {...props} initialStatus={NodeStatus.Running} />
    );

    expect(api.logOptions[api.logOptions.length - 1]).toMatchObject({
      refreshInterval: 0,
    });
    expect(api.itemOptions[api.itemOptions.length - 1]).toMatchObject({
      refreshInterval: 0,
    });
  });

  it('does not poll a body step that had finished when opened', () => {
    api.bodyStatus = 'failed';
    render(
      <ForeachBodyStepLog {...props} initialStatus={NodeStatus.Failed} />,
      { wrapper: UserPreferencesProvider }
    );

    expect(api.logOptions.every((o) => o.refreshInterval === 0)).toBe(true);
  });
});
