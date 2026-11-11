// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import * as React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  useCanManageNotifications,
  useCanWrite,
  useIsAdmin,
} from '@/contexts/AuthContext';
import { ConfigContext, type LicenseStatus } from '@/contexts/ConfigContext';
import { LicenseContext } from '@/contexts/LicenseContext';
import { useQuery } from '@/hooks/api';
import { type GettingStarted, useGettingStarted } from '../useGettingStarted';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));
vi.mock('@/contexts/AuthContext', () => ({
  useCanManageNotifications: vi.fn(),
  useCanWrite: vi.fn(),
  useIsAdmin: vi.fn(),
}));

type Init = { params?: { query: Record<string, unknown> } } | null;

const useQueryMock = vi.mocked(
  useQuery as unknown as (path: string, init: Init) => unknown
);

const community: LicenseStatus = {
  valid: false,
  plan: '',
  expiry: '',
  features: [],
  gracePeriod: false,
  graceEndsAt: '',
  community: true,
  source: '',
  warningCode: '',
  error: '',
};
const licensed: LicenseStatus = {
  ...community,
  community: false,
  valid: true,
  plan: 'team',
};
// A server connected to Dagu Cloud, which MCP clients can reach through it.
const connected: LicenseStatus = { ...licensed, serverId: 'srv-1' };

function dag(
  name: string,
  { ran = false, schedule = false } = {}
): Record<string, unknown> {
  return {
    fileName: name,
    dag: { name, schedule: schedule ? [{ expression: '0 9 * * *' }] : [] },
    latestDAGRun: { dagRunId: ran ? `${name}-1` : '' },
    suspended: false,
  };
}

const examples = [
  'example-01-basic-sequential',
  'example-06-retry-on-failure',
].map((name) => dag(name));

function list(dags: Record<string, unknown>[], totalRecords = dags.length) {
  return { dags, pagination: { totalRecords } };
}

type Served = {
  sample?: object;
  active?: object;
  channels?: object;
  remoteAccess?: object;
  keys?: object;
  failing?: string;
};

const requestNames: Record<string, keyof Served> = {
  '/notification-channels': 'channels',
  '/license/remote-access': 'remoteAccess',
  '/api-keys': 'keys',
};

const noKeys = { apiKeys: [] };
const mcpKey = { apiKeys: [{ id: 'k1', allowedSurfaces: ['mcp'] }] };

// Answers each request from `served`, with no API keys unless it names some;
// `failing` names a request that errors.
function serve(served: Served) {
  const answers: Served = { keys: noKeys, ...served };
  useQueryMock.mockImplementation((path, init) => {
    if (!init) return { data: undefined };
    const name =
      requestNames[path] ?? (init.params?.query.active ? 'active' : 'sample');
    return served.failing === name
      ? { data: undefined, error: new Error('unavailable') }
      : { data: answers[name] };
  });
}

// Each request the hook made, as its path and query.
function requests(): [string, Record<string, unknown>][] {
  const seen = new Map<string, [string, Record<string, unknown>]>();
  for (const [path, init] of useQueryMock.mock.calls) {
    if (init)
      seen.set(JSON.stringify([path, init]), [path, init.params?.query ?? {}]);
  }
  return [...seen.values()];
}

function renderGettingStarted({
  license = community,
  remoteNode = 'local',
  authMode = 'builtin',
}: {
  license?: LicenseStatus;
  remoteNode?: string;
  authMode?: string;
} = {}) {
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <ConfigContext.Provider value={{ authMode } as never}>
      <AppBarContext.Provider
        value={{ selectedRemoteNode: remoteNode } as never}
      >
        <LicenseContext.Provider value={{ license, mutate: vi.fn() }}>
          {children}
        </LicenseContext.Provider>
      </AppBarContext.Provider>
    </ConfigContext.Provider>
  );
  return renderHook(() => useGettingStarted(), { wrapper });
}

function stored(remoteNode = 'local'): string | null {
  return localStorage.getItem(`dagu.gettingStarted:${remoteNode}`);
}

function stepIDs(result: GettingStarted): string[] {
  return result.status === 'visible' ? result.steps.map((step) => step.id) : [];
}

describe('useGettingStarted', () => {
  beforeEach(() => {
    localStorage.clear();
    useQueryMock.mockReset();
    vi.mocked(useCanWrite).mockReturnValue(true);
    vi.mocked(useIsAdmin).mockReturnValue(true);
    vi.mocked(useCanManageNotifications).mockReturnValue(true);
  });

  it('shows a fresh install its first steps', () => {
    serve({ sample: list(examples), channels: { channels: [] } });

    const { result } = renderGettingStarted();

    expect(stepIDs(result.current)).toEqual([
      'run',
      'create',
      'schedule',
      'alert',
      'ai',
    ]);
    expect(result.current).toMatchObject({
      steps: [
        { id: 'run', done: false, exampleFile: 'example-06-retry-on-failure' },
        {},
        {},
        { id: 'alert', canConnect: true, canAddChannel: true },
        { id: 'ai', done: false, canUseCloud: true, canCreateKey: true },
      ],
    });
    expect(stored()).toBe('started');
  });

  it('waits for its data', () => {
    serve({});

    const { result } = renderGettingStarted();

    expect(result.current.status).toBe('pending');
    expect(stored()).toBeNull();
  });

  it('asks nothing of a user who cannot write', () => {
    vi.mocked(useCanWrite).mockReturnValue(false);
    serve({ sample: list(examples) });

    const { result } = renderGettingStarted();

    expect(result.current.status).toBe('hidden');
    expect(requests()).toEqual([]);
  });

  it('asks nothing once dismissed', () => {
    localStorage.setItem('dagu.gettingStarted:local', 'dismissed');
    serve({ sample: list(examples) });

    const { result } = renderGettingStarted();

    expect(result.current.status).toBe('hidden');
    expect(requests()).toEqual([]);
  });

  it('stays dismissed after the dismiss action', () => {
    serve({ sample: list(examples), channels: { channels: [] } });
    const { result } = renderGettingStarted();

    act(() => {
      if (result.current.status === 'visible') result.current.dismiss();
    });

    expect(result.current.status).toBe('hidden');
    expect(stored()).toBe('dismissed');
  });

  it('looks up schedules only when the sample misses workflows', () => {
    serve({
      sample: list(examples, 12),
      active: list([], 0),
      channels: { channels: [] },
    });

    const { result } = renderGettingStarted();

    expect(requests()).toContainEqual([
      '/dags',
      { active: true, perPage: 1, remoteNode: 'local', workspace: 'all' },
    ]);
    expect(result.current).toMatchObject({
      steps: [{}, { done: true }, { id: 'schedule', done: false }, {}, {}],
    });
  });

  it('reads schedules from a complete sample', () => {
    serve({ sample: list(examples), channels: { channels: [] } });

    renderGettingStarted();

    expect(
      requests().filter(([, query]) => query.active !== undefined)
    ).toEqual([]);
  });

  it.each([
    ['a licensed server', licensed, true],
    ['a user who cannot manage notifications', community, false],
  ])('skips channels for %s', (_, license, canManage) => {
    vi.mocked(useCanManageNotifications).mockReturnValue(canManage);
    serve({ sample: list(examples) });

    const { result } = renderGettingStarted({ license });

    expect(
      requests().filter(([path]) => path === '/notification-channels')
    ).toEqual([]);
    expect(result.current.status).toBe('visible');
  });

  it('leaves out alerts and AI apps for a user who cannot set them up', () => {
    vi.mocked(useIsAdmin).mockReturnValue(false);
    vi.mocked(useCanManageNotifications).mockReturnValue(false);
    serve({ sample: list(examples) });

    const { result } = renderGettingStarted();

    expect(stepIDs(result.current)).toEqual(['run', 'create', 'schedule']);
  });

  it('counts an API key that MCP clients may use as a connected AI app', () => {
    serve({ sample: list(examples), channels: { channels: [] }, keys: mcpKey });

    const { result } = renderGettingStarted();

    expect(result.current).toMatchObject({
      steps: [{}, {}, {}, {}, { id: 'ai', done: true }],
    });
  });

  it('counts remote access as a connected AI app on a connected server', () => {
    serve({ sample: list(examples), remoteAccess: { level: 'viewer' } });

    const { result } = renderGettingStarted({ license: connected });

    expect(requests()).toContainEqual([
      '/license/remote-access',
      { remoteNode: 'local' },
    ]);
    expect(result.current).toMatchObject({
      steps: [{}, {}, {}, {}, { id: 'ai', done: true, canUseCloud: true }],
    });
  });

  it('hides without remembering when the remote access request fails', () => {
    serve({ sample: list(examples), failing: 'remoteAccess' });

    const { result } = renderGettingStarted({ license: connected });

    expect(result.current.status).toBe('hidden');
    expect(stored()).toBeNull();
  });

  // API keys are issued by the local server's built-in auth only.
  it.each([
    ['without built-in auth', { authMode: 'basic' }],
    ['on a remote node', { remoteNode: 'edge' }],
  ])('offers only Dagu Cloud for MCP clients %s', (_, options) => {
    serve({ sample: list(examples), channels: { channels: [] } });

    const { result } = renderGettingStarted(options);

    expect(requests().filter(([path]) => path === '/api-keys')).toEqual([]);
    expect(result.current).toMatchObject({
      steps: [{}, {}, {}, {}, { id: 'ai', canCreateKey: false }],
    });
  });

  // An upgraded server already runs its own scheduled workflows.
  it('never shows on a server already in use', () => {
    serve({
      sample: list([...examples, dag('etl', { ran: true, schedule: true })]),
      channels: { channels: [] },
    });

    const { result } = renderGettingStarted();

    expect(result.current.status).toBe('hidden');
    expect(stored()).toBe('dismissed');
  });

  it('remembers that every step is done', () => {
    localStorage.setItem('dagu.gettingStarted:local', 'started');
    serve({
      sample: list([dag('etl', { ran: true, schedule: true })]),
      channels: { channels: [{ id: 'slack' }] },
      keys: mcpKey,
    });
    const { result, unmount } = renderGettingStarted();
    expect(result.current.status).toBe('hidden');
    expect(stored()).toBe('done');
    unmount();
    useQueryMock.mockClear();

    // A step undone later does not bring the checklist back.
    serve({ sample: list(examples), channels: { channels: [] } });
    const again = renderGettingStarted();

    expect(again.result.current.status).toBe('hidden');
    expect(requests()).toEqual([]);
  });

  it.each(['sample', 'active', 'channels', 'keys'])(
    'hides without remembering when the %s request fails',
    (failing) => {
      serve({
        sample: list(examples, 12),
        active: list([], 0),
        channels: { channels: [] },
        failing,
      });

      const { result } = renderGettingStarted();

      expect(result.current.status).toBe('hidden');
      expect(stored()).toBeNull();
    }
  );

  it('sends the selected remote node with every request', () => {
    serve({
      sample: list(examples, 12),
      active: list([], 0),
      channels: { channels: [] },
    });

    renderGettingStarted({ remoteNode: 'edge' });

    expect(requests()).toHaveLength(3);
    for (const [, query] of requests()) {
      expect(query.remoteNode).toBe('edge');
    }
    expect(stored('edge')).toBe('started');
  });
});
