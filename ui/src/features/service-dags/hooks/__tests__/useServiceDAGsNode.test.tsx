// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import type React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext } from '@/contexts/ConfigContext';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import { WorkspaceKind } from '@/lib/workspace';
import { useServiceDAGs } from '../useServiceDAGs';

// The live stream runs for real down to the manager, which records the node
// of each subscription.
const h = vi.hoisted(() => ({
  inits: [] as unknown[],
  topics: [] as Array<{ endpoint: string; remoteNode: string }>,
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (_path: string, init: unknown) => {
    h.inits.push(init);
    return { data: undefined, error: undefined, mutate: vi.fn() };
  },
}));

vi.mock('@/hooks/SSEManager', () => ({
  sseManager: {
    subscribe: (endpoint: string, remoteNode: string) => {
      h.topics.push({ endpoint, remoteNode });
      return vi.fn();
    },
  },
}));

function AppBar({ children }: { children: React.ReactNode }) {
  return (
    <ConfigContext.Provider value={{ apiURL: '/api/v1' } as never}>
      <AppBarContext.Provider
        value={
          {
            selectedRemoteNode: 'bar-node',
            workspaceSelection: {
              kind: WorkspaceKind.workspace,
              workspace: 'ops',
            },
          } as never
        }
      >
        {children}
      </AppBarContext.Provider>
    </ConfigContext.Provider>
  );
}

function Scoped({ children }: { children: React.ReactNode }) {
  return (
    <AppBar>
      <RemoteNodeProvider remoteNode="scoped-node">
        {children}
      </RemoteNodeProvider>
    </AppBar>
  );
}

describe('the node of useServiceDAGs', () => {
  beforeEach(() => {
    h.inits = [];
    h.topics = [];
  });

  it('reads the list and its live topic of the node of the app bar', () => {
    renderHook(() => useServiceDAGs(), { wrapper: AppBar });

    expect(h.inits).toContainEqual({
      params: { query: { remoteNode: 'bar-node', workspace: 'ops' } },
    });
    expect(h.topics).toEqual([
      {
        endpoint: '/events/service-dags?workspace=ops',
        remoteNode: 'bar-node',
      },
    ]);
  });

  // A page of another node than the app bar: both reads follow the page.
  it('reads the list and its live topic of the scoped node', () => {
    renderHook(() => useServiceDAGs(), { wrapper: Scoped });

    expect(h.inits).toContainEqual({
      params: { query: { remoteNode: 'scoped-node', workspace: 'ops' } },
    });
    expect(h.inits).not.toContainEqual({
      params: { query: { remoteNode: 'bar-node', workspace: 'ops' } },
    });
    expect(h.topics).toEqual([
      {
        endpoint: '/events/service-dags?workspace=ops',
        remoteNode: 'scoped-node',
      },
    ]);
  });
});
