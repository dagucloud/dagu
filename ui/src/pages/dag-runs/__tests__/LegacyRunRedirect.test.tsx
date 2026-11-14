// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import LegacyRunRedirect from '../LegacyRunRedirect';

// The latest run of a DAG, as the run endpoint answers for 'latest'.
const h = vi.hoisted(() => ({
  latest: undefined as { data?: unknown; error?: unknown } | undefined,
  calls: [] as unknown[],
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (_path: string, init: unknown) => {
    if (init === null) return { data: undefined };
    h.calls.push(init);
    return h.latest ?? { data: undefined };
  },
}));

function LocationProbe() {
  const location = useLocation();
  return (
    <div data-testid="location">{`${location.pathname}${location.search}`}</div>
  );
}

function renderAt(entry: string, selectedRemoteNode?: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <AppBarContext.Provider value={{ selectedRemoteNode } as never}>
        <Routes>
          <Route
            path="/dag-runs/:name/:dagRunId"
            element={<LegacyRunRedirect />}
          />
          <Route path="*" element={<LocationProbe />} />
        </Routes>
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  h.latest = undefined;
  h.calls = [];
});
afterEach(cleanup);

// parity:run-page-route
describe('LegacyRunRedirect', () => {
  it('sends the run page to the workflow page with ?run', () => {
    renderAt('/dag-runs/etl/run-1?remoteNode=edge&workspace=ops');

    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/etl?remoteNode=edge&workspace=ops&run=run-1'
    );
  });

  it('encodes the names and takes the node of the app bar', () => {
    renderAt('/dag-runs/my%20flow/run%201', 'edge-b');

    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/my%20flow?remoteNode=edge-b&run=run+1'
    );
  });

  // The workflow page finds the file of a run by its ID: a DAG whose name
  // differs from its file is found through the latest run.
  it('names the latest run by its ID', () => {
    h.latest = { data: { dagRunDetails: { dagRunId: 'run-9' } } };
    renderAt('/dag-runs/nightly/latest');

    expect(h.calls).toEqual([
      {
        params: {
          path: { name: 'nightly', dagRunId: 'latest' },
          query: { remoteNode: 'local' },
        },
      },
    ]);
    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/nightly?remoteNode=local&run=run-9'
    );
  });

  it('drops ?run when the DAG has no latest run', () => {
    h.latest = { data: undefined, error: { code: 'not_found' } };
    renderAt('/dag-runs/etl/latest');

    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/etl?remoteNode=local'
    );
  });

  // The form of the sub-run links of notifications and incidents. Their step
  // is a step of the sub-run.
  it('opens the sub-run of a sub-run link over its root run', () => {
    renderAt(
      '/dag-runs/root/root-run?subDAGRunId=child&dagRunId=root-run&dagRunName=root&remoteNode=local&step=load'
    );

    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/root?remoteNode=local&run=root-run&subRun=child&step=load'
    );
  });

  // The old tab of a sub-run link was a tab of the sub-run: the dock of the
  // root run does not take it.
  it('keeps the tab of a sub-run link off the dock of its root', () => {
    renderAt(
      '/dag-runs/root/root-run?subDAGRunId=child&dagRunId=root-run&dagRunName=root&tab=artifacts'
    );

    expect(screen.getByTestId('location')).toHaveTextContent(
      /^\/dags\/root\?remoteNode=local&run=root-run&subRun=child$/
    );
  });

  it('turns the old tab into a dock tab and keeps the step', () => {
    renderAt('/dag-runs/etl/run-1?tab=artifacts&step=load');

    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/etl?remoteNode=local&run=run-1&step=load&dock=artifacts'
    );
  });

  it('shows the action tab for an approval link', () => {
    renderAt('/dag-runs/etl/run-1?tab=approval');

    expect(screen.getByTestId('location')).toHaveTextContent('dock=actions');
  });
});
