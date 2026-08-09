// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React, { useEffect } from 'react';
import { describe, expect, it, vi } from 'vitest';

const sseCalls: Array<{ params: Record<string, unknown>; enabled: boolean }> =
  [];

vi.mock('@/hooks/useDAGsListSSE', () => ({
  useDAGsListSSE: (params: Record<string, unknown>, enabled: boolean) => {
    sseCalls.push({ params, enabled });
    return { connected: false, data: null };
  },
}));

const listData = {
  dags: [
    {
      fileName: 'daily-etl',
      dag: { name: 'filename-collision' },
      latestDAGRun: { status: 4, statusLabel: 'finished' },
      suspended: false,
      errors: [],
    },
    {
      fileName: 'etl.yaml',
      dag: { name: 'daily-etl' },
      latestDAGRun: { status: 4, statusLabel: 'finished' },
      suspended: false,
      errors: [],
    },
  ],
  errors: [],
  pagination: { totalRecords: 2 },
};

vi.mock('@/hooks/api', () => ({
  useQuery: (_path: string, init: unknown) => ({
    data: init === null ? undefined : listData,
    mutate: vi.fn(),
    isLoading: false,
  }),
  useClient: () => ({}),
}));

vi.mock('@/hooks/useSSECacheSync', () => ({
  useSSECacheSync: () => {},
  sseFallbackOptions: () => ({}),
}));

vi.mock('@/contexts/AppBarContext', async () => {
  const { createContext } = await import('react');
  return {
    AppBarContext: createContext({ selectedRemoteNode: 'node-a' }),
  };
});

import { WikiLiveProvider } from '../WikiLiveProvider';
import { useWikiLive } from '../context';

function Probe({ dagRef }: { dagRef: string }) {
  const live = useWikiLive();
  useEffect(() => {
    if (!live) return;
    return live.registerRef(dagRef);
  }, [live, dagRef]);
  if (!live) return <span>no-provider</span>;
  const result = live.lookup(dagRef);
  return (
    <span>
      {dagRef}:{result.state}
      {result.state === 'found' ? `:${result.fileName}` : ''}
    </span>
  );
}

describe('WikiLiveProvider', () => {
  it('resolves refs by DAG name and file name from one shared feed', () => {
    sseCalls.length = 0;
    render(
      <WikiLiveProvider workspace="ops">
        <Probe dagRef="daily-etl" />
        <Probe dagRef="etl.yaml" />
        <Probe dagRef="missing" />
      </WikiLiveProvider>
    );

    expect(screen.getByText('daily-etl:found:etl.yaml')).toBeInTheDocument();
    expect(screen.getByText('etl.yaml:found:etl.yaml')).toBeInTheDocument();
    expect(screen.getByText('missing:not-found')).toBeInTheDocument();

    // One shared subscription threads workspace and remoteNode; three
    // mounted refs never add topics.
    const enabledCalls = sseCalls.filter((c) => c.enabled);
    expect(enabledCalls.length).toBeGreaterThan(0);
    for (const call of sseCalls) {
      expect(call.params).toMatchObject({
        remoteNode: 'node-a',
        workspace: 'ops',
      });
    }
  });

  it('keeps the feed disabled with no live elements mounted', () => {
    sseCalls.length = 0;
    render(
      <WikiLiveProvider workspace={null}>
        <span>static content</span>
      </WikiLiveProvider>
    );

    expect(sseCalls.length).toBeGreaterThan(0);
    expect(sseCalls.every((c) => !c.enabled)).toBe(true);
  });

  it('scopes the default page scope to the default workspace, never all', () => {
    sseCalls.length = 0;
    render(
      <WikiLiveProvider workspace={null}>
        <Probe dagRef="daily-etl" />
      </WikiLiveProvider>
    );

    expect(sseCalls.length).toBeGreaterThan(0);
    for (const call of sseCalls) {
      expect(call.params).toMatchObject({ workspace: 'default' });
    }
  });

  it('renders a no-provider marker outside the provider', () => {
    render(<Probe dagRef="daily-etl" />);
    expect(screen.getByText('no-provider')).toBeInTheDocument();
  });
});
