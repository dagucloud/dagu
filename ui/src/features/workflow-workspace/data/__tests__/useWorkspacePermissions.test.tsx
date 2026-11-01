// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkspacePermissions } from '../types';
import { useWorkspacePermissions } from '../useWorkspacePermissions';
import { dagPage, idleSSE, run } from './fixtures';
import { renderWorkspace, serve } from './harness';

const h = vi.hoisted(() => ({
  GET: vi.fn(),
  gates: {
    canWrite: false,
    canRunDags: false,
    canExecute: false,
    canExecuteForWorkspace: false,
    isAdmin: false,
    canManageProfiles: false,
    canManageWebhooks: false,
    canManageNotifications: false,
  },
  workspaceArgs: [] as unknown[],
  remoteNode: 'local',
}));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return {
    useQuery: createQueryHook({ GET: h.GET } as never, '/'),
    useClient: () => ({ GET: h.GET }),
  };
});
vi.mock('@/hooks/useDAGRunSSE', () => ({ useDAGRunSSE: () => idleSSE }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => h.remoteNode,
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ permissions: { runDags: h.gates.canRunDags } }),
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: (workspace: unknown) => {
    h.workspaceArgs.push(['write', workspace]);
    return h.gates.canWrite;
  },
  useCanExecute: () => h.gates.canExecute,
  useCanExecuteForWorkspace: (workspace: unknown) => {
    h.workspaceArgs.push(['execute', workspace]);
    return h.gates.canExecuteForWorkspace;
  },
  useIsAdmin: () => h.gates.isAdmin,
  useCanManageProfiles: () => h.gates.canManageProfiles,
  useCanManageWebhooks: () => h.gates.canManageWebhooks,
  useCanManageNotifications: () => h.gates.canManageNotifications,
}));

const NONE: WorkspacePermissions = {
  canWrite: false,
  canRunDags: false,
  canExecute: false,
  canExecuteForWorkspace: false,
  isAdmin: false,
  canManageProfiles: false,
  canManageWebhooks: false,
  canManageNotifications: false,
};

const GATES = Object.keys(NONE) as (keyof WorkspacePermissions)[];

beforeEach(() => {
  Object.assign(h.gates, NONE);
  h.workspaceArgs.length = 0;
  h.remoteNode = 'local';
  h.GET.mockReset();
  h.GET.mockImplementation(
    serve({
      dag: dagPage(run('run-1')),
      runs: {},
      history: { dagRuns: [], gridData: [] },
      subRuns: [],
    })
  );
});

// parity:perm-hooks
describe('useWorkspacePermissions', () => {
  it.each(GATES)('reads %s from its own source only', async (gate) => {
    h.gates[gate] = true;
    const view = renderWorkspace(() => useWorkspacePermissions(), {
      workspaceName: 'team-a',
    });
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    expect(view.result.current.value).toEqual({ ...NONE, [gate]: true });
  });

  it('gives the workspace name to the workspace gates', async () => {
    const view = renderWorkspace(() => useWorkspacePermissions(), {
      workspaceName: 'team-a',
    });
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    expect(h.workspaceArgs).toContainEqual(['write', 'team-a']);
    expect(h.workspaceArgs).toContainEqual(['execute', 'team-a']);
    expect(
      h.workspaceArgs.every((args) => (args as unknown[])[1] === 'team-a')
    ).toBe(true);
  });

  // The webhook gate describes the local server. On a remote node the
  // webhook read can answer 401, and a 401 signs the user out.
  it('denies webhook management on a remote node', async () => {
    Object.assign(h.gates, {
      canManageWebhooks: true,
      canManageProfiles: true,
    });
    h.remoteNode = 'node-b';
    const view = renderWorkspace(() => useWorkspacePermissions(), {
      workspaceName: 'team-a',
    });
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    expect(view.result.current.value).toEqual({
      ...NONE,
      canManageProfiles: true,
    });
  });
});
