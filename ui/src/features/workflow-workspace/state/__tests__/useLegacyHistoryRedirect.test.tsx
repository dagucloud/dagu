// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Legacy /history?idx=n links open the same run on the workflow page (?run).
// The cases port the DAGExecutionHistory pins of T06.
// parity:history-selection-url

import { act, render } from '@testing-library/react';
import type { ReactNode } from 'react';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigationType,
} from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import type { ApiDAGRunDetails } from '../../contracts';
import { run } from '../../data/__tests__/fixtures';
import { useLegacyHistoryRedirect } from '../useLegacyHistoryRedirect';

// The server sends the runs newest first; the old grid showed r1 on the left.
const NEWEST_FIRST = [run('r3'), run('r2'), run('r1')];

interface Seen {
  pathname: string;
  params: URLSearchParams;
  navigationType: string;
  pending: boolean;
}

function Probe({
  history,
  seen,
}: {
  history: { runs: ApiDAGRunDetails[]; isLoading: boolean };
  seen: Seen[];
}) {
  const pending = useLegacyHistoryRedirect(history);
  const location = useLocation();
  const navigationType = useNavigationType();
  seen.push({
    pathname: location.pathname,
    params: new URLSearchParams(location.search),
    navigationType,
    pending,
  });
  return null;
}

function setup(
  url: string,
  history: { runs: ApiDAGRunDetails[]; isLoading: boolean } = {
    runs: NEWEST_FIRST,
    isLoading: false,
  }
) {
  const seen: Seen[] = [];
  const tree = (h: typeof history): ReactNode => (
    <MemoryRouter initialEntries={['/start', url]} initialIndex={1}>
      <Routes>
        <Route
          path="/dags/:fileName/:tab?"
          element={<Probe history={h} seen={seen} />}
        />
      </Routes>
    </MemoryRouter>
  );
  const view = render(tree(history));
  return {
    seen,
    last: () => seen[seen.length - 1]!,
    load: (runs: ApiDAGRunDetails[]) =>
      act(() => {
        view.rerender(tree({ runs, isLoading: false }));
      }),
  };
}

describe('useLegacyHistoryRedirect', () => {
  // The T06 DAGExecutionHistory cases, ported. Out of range gives the newest run.
  it.each([
    ['idx 0 is the oldest column', '?idx=0', 'r1'],
    ['idx 1 is the middle column', '?idx=1', 'r2'],
    ['idx 2 is the newest column', '?idx=2', 'r3'],
    ['an idx past the end gives the newest run', '?idx=9', 'r3'],
    ['a negative idx gives the newest run', '?idx=-4', 'r3'],
    ['an idx that is not a number gives the newest run', '?idx=abc', 'r3'],
    ['dagRunId wins over idx', '?idx=2&dagRunId=r1', 'r1'],
  ])('%s', (_, search, runId) => {
    const { last } = setup(`/dags/etl/history${search}`);

    const { pathname, params, navigationType } = last();
    expect(pathname).toBe('/dags/etl');
    expect(params.get('run')).toBe(runId);
    // D6: a link to a run opens the Run panel, which ?dock does after mount.
    expect(params.get('dock')).toBe('timeline');
    expect(params.has('view')).toBe(false);
    expect(params.has('idx')).toBe(false);
    expect(params.has('dagRunId')).toBe(false);
    expect(navigationType).toBe('REPLACE');
  });

  it('keeps the default newest selection when there is no idx', () => {
    const { seen, last } = setup('/dags/etl/executions');
    expect(last().pathname).toBe('/dags/etl/executions');
    expect(last().params.has('run')).toBe(false);
    expect(seen.every((s) => !s.pending)).toBe(true);
  });

  it('also resolves an idx on the executions path', () => {
    const { last } = setup('/dags/etl/executions?idx=0');
    expect(last().params.get('run')).toBe('r1');
    expect(last().params.has('view')).toBe(false);
  });

  it('leaves the URL alone when ?run is present', () => {
    const { last } = setup('/dags/etl/executions?idx=0&run=r2');
    expect(last().params.get('run')).toBe('r2');
    expect(last().params.get('idx')).toBe('0');
    expect(last().navigationType).toBe('POP');
  });

  it('does nothing on another tab', () => {
    const { last } = setup('/dags/etl/webhook?idx=0');
    expect(last().pathname).toBe('/dags/etl/webhook');
    expect(last().params.get('idx')).toBe('0');
  });

  it('waits for the first history load', () => {
    const { last, load } = setup('/dags/etl/history?idx=0', {
      runs: [],
      isLoading: true,
    });
    expect(last().pathname).toBe('/dags/etl/history');
    expect(last().pending).toBe(true);

    load(NEWEST_FIRST);

    expect(last().pathname).toBe('/dags/etl');
    expect(last().params.get('run')).toBe('r1');
    expect(last().pending).toBe(false);
  });

  it('does not wait for the history when dagRunId names the run', () => {
    const { last } = setup('/dags/etl/history?idx=0&dagRunId=r2', {
      runs: [],
      isLoading: true,
    });
    expect(last().params.get('run')).toBe('r2');
  });

  // N4 review: an out-of-range idx resolves once, with no clamp loop on ?idx.
  it('navigates once for an out-of-range idx', () => {
    const { seen, last } = setup('/dags/etl/history?idx=99');
    const urls = new Set(
      seen.map((s) => `${s.pathname}?${s.params.toString()}`)
    );
    expect(urls.size).toBe(2);
    expect(last().params.get('run')).toBe('r3');
    expect(last().params.has('idx')).toBe(false);
  });

  it('opens the latest run when the history is empty', () => {
    const { last } = setup('/dags/etl/history?idx=0', {
      runs: [],
      isLoading: false,
    });
    expect(last().pathname).toBe('/dags/etl');
    expect(last().params.has('run')).toBe(false);
    expect(last().params.has('dock')).toBe(false);
    expect(last().params.has('view')).toBe(false);
  });

  it('keeps remoteNode and workspace, and drops the other keys', () => {
    const { last } = setup(
      '/dags/etl/history?idx=1&remoteNode=edge&workspace=ops&ui=workspace&dagRunName=etl&foo=1'
    );
    const { params } = last();
    expect(Object.fromEntries(params)).toEqual({
      run: 'r2',
      dock: 'timeline',
      remoteNode: 'edge',
      workspace: 'ops',
    });
  });
});
