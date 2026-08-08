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
      fileName: 'etl.yaml',
      dag: { name: 'daily-etl' },
      latestDAGRun: { status: 4, statusLabel: 'finished' },
      suspended: false,
      errors: [],
    },
  ],
  errors: [],
  pagination: { totalRecords: 1 },
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

import { DocLiveProvider } from '../DocLiveProvider';
import { useDocLive } from '../context';

function Probe({ dagRef }: { dagRef: string }) {
  const live = useDocLive();
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

describe('DocLiveProvider', () => {
  it('resolves refs by DAG name and file name from one shared feed', () => {
    sseCalls.length = 0;
    render(
      <DocLiveProvider workspace="ops">
        <Probe dagRef="daily-etl" />
        <Probe dagRef="etl.yaml" />
        <Probe dagRef="missing" />
      </DocLiveProvider>
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
      <DocLiveProvider workspace={null}>
        <span>static content</span>
      </DocLiveProvider>
    );

    expect(sseCalls.every((c) => !c.enabled)).toBe(true);
  });

  it('scopes the default doc scope to the default workspace, never all', () => {
    sseCalls.length = 0;
    render(
      <DocLiveProvider workspace={null}>
        <Probe dagRef="daily-etl" />
      </DocLiveProvider>
    );

    for (const call of sseCalls) {
      expect(call.params).toMatchObject({ workspace: 'default' });
    }
  });

  it('renders a no-provider marker outside the provider', () => {
    render(<Probe dagRef="daily-etl" />);
    expect(screen.getByText('no-provider')).toBeInTheDocument();
  });
});
