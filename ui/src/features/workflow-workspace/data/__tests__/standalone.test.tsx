// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useWorkspaceUrl } from '../../state/workspaceUrl';
import { standalonePageData } from '../standalonePageData';
import { dagPage, FILE_NAME, idleSSE, node, run } from './fixtures';
import { renderWorkspace, serve } from './harness';

const h = vi.hoisted(() => ({ GET: vi.fn() }));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return {
    useQuery: createQueryHook({ GET: h.GET } as never, '/'),
    useClient: () => ({ GET: h.GET }),
  };
});
vi.mock('@/hooks/useDAGRunSSE', () => ({ useDAGRunSSE: () => idleSSE }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

// A run whose workflow file is gone shows in the workspace, drawn from the
// definition that it stored.
describe('a run without its workflow file', () => {
  it('gives the page the DAG as the run stored it', () => {
    const orphan = run('run-9', [node('fetch'), node('report')], undefined, {
      onFailure: node('alert'),
    });
    const page = standalonePageData(orphan);
    expect(page.dag?.name).toBe(orphan.name);
    expect(page.dag?.steps?.map((step) => step.name)).toEqual([
      'fetch',
      'report',
    ]);
    expect(page.dag?.handlerOn?.failure?.name).toBe('alert');
    expect(page.latestDAGRun).toBe(orphan);
  });

  // The page has no other definition than the run's own.
  it('keeps the page on the definition of the run', async () => {
    const orphan = run('run-9');
    h.GET.mockImplementation(
      serve({
        dag: dagPage(orphan),
        runs: { 'run-9': orphan },
        history: { dagRuns: [], gridData: [] },
        subRuns: [],
      })
    );
    const view = renderWorkspace(() => useWorkspaceUrl()[0].def, {
      url: `/dags/${FILE_NAME}?run=run-9`,
      standaloneRun: orphan,
    });
    await waitFor(() => expect(view.result.current?.value).toBe('run'));
  });
});
