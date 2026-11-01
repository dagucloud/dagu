// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { waitFor } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useRunHistory } from '../useRunHistory';
import { useSubRuns } from '../useSubRuns';
import { useWorkspaceRun } from '../useWorkspaceRun';
import {
  APP_WORKSPACE_PREFIXES,
  dagPage,
  DAG_NAME,
  idleSSE,
  run,
} from './fixtures';
import { renderWorkspace, serve } from './harness';

const h = vi.hoisted(() => ({ GET: vi.fn(), keys: [] as unknown[][] }));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  const realUseQuery = createQueryHook({ GET: h.GET } as never, '/');
  return {
    useQuery: (path: unknown, init: unknown, config: unknown) => {
      // swr-openapi builds the SWR key as [prefix, path, init].
      if (init !== null) h.keys.push(['/', path, init]);
      return (realUseQuery as (...a: unknown[]) => unknown)(path, init, config);
    },
    useClient: () => ({ GET: h.GET }),
  };
});
vi.mock('@/hooks/useDAGRunSSE', () => ({ useDAGRunSSE: () => idleSSE }));
vi.mock('@/hooks/useDAGHistorySSE', () => ({
  useDAGHistorySSE: () => idleSSE,
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

/** The key test of App.tsx (isWorkspaceScopedSWRKey), path part. */
function isWorkspaceScoped(key: unknown[]): boolean {
  const path = key[1];
  return (
    key.length >= 3 &&
    typeof path === 'string' &&
    APP_WORKSPACE_PREFIXES.some((prefix) => path.startsWith(prefix))
  );
}

beforeEach(() => {
  h.keys.length = 0;
  h.GET.mockReset();
  h.GET.mockImplementation(
    serve({
      dag: dagPage(run('run-1')),
      runs: { 'run-0': run('run-0') },
      history: { dagRuns: [], gridData: [] },
      subRuns: [],
    })
  );
});

// parity:shell-workspace-switch-revalidate parity:api-dag-details
describe('workspace SWR keys', () => {
  it('match the App.tsx workspace revalidation prefixes', async () => {
    const view = renderWorkspace(
      () => ({
        run: useWorkspaceRun(),
        history: useRunHistory({ enabled: true }),
        subRuns: useSubRuns({ name: DAG_NAME, runId: 'run-0' }, 'child'),
      }),
      { url: '/dags/demo?run=run-0' }
    );
    await waitFor(() =>
      expect(view.result.current?.value.run.details?.dagRunId).toBe('run-0')
    );
    const paths = new Set(h.keys.map((key) => key[1]));
    expect(paths).toEqual(
      new Set([
        '/dags/{fileName}',
        '/dag-runs/{name}/{dagRunId}',
        '/dags/{fileName}/dag-runs',
        '/dag-runs/{name}/{dagRunId}/sub-dag-runs',
      ])
    );
    for (const key of h.keys) {
      expect(isWorkspaceScoped(key)).toBe(true);
    }
  });

  it('uses the prefixes that App.tsx declares', () => {
    const app = readFileSync(resolve(__dirname, '../../../../App.tsx'), 'utf8');
    for (const prefix of APP_WORKSPACE_PREFIXES) {
      expect(app).toContain(`'${prefix}',`);
    }
    expect(app).toContain('WORKSPACE_SENSITIVE_TARGET_PATH_PREFIXES');
  });
});
