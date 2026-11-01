// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, ReusedInputVia, Status } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type { ApiDAGRunDetails } from '../../contracts';
import { node, run } from '../../data/__tests__/fixtures';
import { CachedLogs, cachedSteps } from '../CachedLogs';

type Call = { path: string; params: Record<string, unknown> };
const h = vi.hoisted(() => ({
  calls: [] as Call[],
  // The step log of a source run, by "<runId>/<step>/<stream>".
  logs: {} as Record<string, { content: string; totalLines: number } | 'gone'>,
}));

vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/hooks/api', () => ({
  useQuery: (
    path: string,
    init: {
      params: { path: Record<string, string>; query: { stream: string } };
    } | null
  ) => {
    if (!init) return { data: undefined, error: undefined, isLoading: false };
    h.calls.push({ path, params: init.params as never });
    const { dagRunId, stepName } = init.params.path;
    const entry = h.logs[`${dagRunId}/${stepName}/${init.params.query.stream}`];
    if (entry === 'gone') {
      return {
        data: undefined,
        error: new Error('not found'),
        isLoading: false,
      };
    }
    return { data: entry, error: undefined, isLoading: false };
  },
}));

const SOURCE = '034mroF1udXVyvPz00bn5a';

/** `late` ran after `early` in the source run, but comes first in the nodes. */
function reusedRun(): ApiDAGRunDetails {
  const record = (step: string, start: string, end: string) => ({
    step,
    runId: SOURCE,
    startedAt: start,
    finishedAt: end,
    via: ReusedInputVia.run,
  });
  return run(
    'run-1',
    [
      node('late', NodeStatus.Skipped),
      node('early', NodeStatus.Skipped),
      node('use', NodeStatus.Success),
    ],
    Status.Success,
    {
      reusedInputs: [
        record('late', '2026-01-01T00:00:10Z', '2026-01-01T00:00:20Z'),
        record('early', '2026-01-01T00:00:00Z', '2026-01-01T00:00:10Z'),
      ],
    }
  );
}

function renderCached(
  details = reusedRun(),
  mode: 'activity' | 'raw' = 'activity'
) {
  return render(<CachedLogs details={details} mode={mode} />, {
    wrapper: UserPreferencesProvider,
  });
}

const stepCalls = () =>
  h.calls.filter((call) => call.path.endsWith('/steps/{stepName}/log'));
const lines = () => screen.queryAllByTestId('cached-log-line');

beforeEach(() => {
  h.calls = [];
  h.logs = {};
  localStorage.clear();
});

describe('cachedSteps', () => {
  it('orders the reused steps by their start in the source run', () => {
    expect(cachedSteps(reusedRun()).map((s) => s.step)).toEqual([
      'early',
      'late',
    ]);
  });
});

// The cached lines are lines of the log of the run: no summary, no group.
// They say "cached" where a line of the run has its time.
describe('CachedLogs', () => {
  it('shows no block for a run that reused nothing', () => {
    renderCached(run('run-1', [node('a')]));
    expect(screen.queryByTestId('cached-logs')).not.toBeInTheDocument();
  });

  it('reads the log of each reused step from its source run, at once', () => {
    renderCached();
    const reads = stepCalls();
    expect(reads.length).toBeGreaterThan(0);
    for (const read of reads) {
      expect(read.params).toMatchObject({ path: { dagRunId: SOURCE } });
    }
    expect(
      new Set(
        reads.map(
          (read) =>
            (
              read.params as never as Call['params'] & {
                path: { stepName: string };
              }
            ).path.stepName
        )
      )
    ).toEqual(new Set(['early', 'late']));
  });

  it('puts the lines in the order that the steps ran, with "cached" for the time', () => {
    h.logs[`${SOURCE}/early/stdout`] = { content: 'one\ntwo\n', totalLines: 2 };
    h.logs[`${SOURCE}/late/stdout`] = { content: 'three\n', totalLines: 1 };
    renderCached();
    expect(lines().map((line) => line.textContent)).toEqual([
      'cachedearlyone',
      'cachedearlytwo',
      'cachedlatethree',
    ]);
    for (const line of lines()) {
      expect(within(line).getByText('cached')).toHaveAttribute(
        'title',
        'Cached from run 034mroF1'
      );
    }
    expect(screen.getAllByText('one')[0]).toHaveClass('text-muted-foreground');
  });

  it('goes the other way when the newest line is at the top', () => {
    localStorage.setItem(
      'user_preferences',
      JSON.stringify({ logOrder: 'newToOld' })
    );
    h.logs[`${SOURCE}/early/stdout`] = { content: 'one\ntwo\n', totalLines: 2 };
    h.logs[`${SOURCE}/late/stdout`] = { content: 'three\n', totalLines: 1 };
    renderCached();
    expect(lines().map((line) => line.dataset.step)).toEqual([
      'late',
      'early',
      'early',
    ]);
    expect(lines().map((line) => line.textContent)).toEqual([
      'cachedlatethree',
      'cachedearlytwo',
      'cachedearlyone',
    ]);
  });

  it('shows the raw lines with "cached" in the place of the line number', () => {
    h.logs[`${SOURCE}/early/stdout`] = { content: 'one\n', totalLines: 1 };
    renderCached(reusedRun(), 'raw');
    const [line] = lines();
    expect(line).toHaveTextContent('cachedone');
    expect(within(line!).getByText('cached')).toHaveClass('w-14');
  });

  it('says so when the source run is gone', () => {
    h.logs[`${SOURCE}/early/stdout`] = 'gone';
    h.logs[`${SOURCE}/early/stderr`] = 'gone';
    renderCached();
    expect(screen.getByTestId('cached-log-gone')).toHaveTextContent(
      'Log of run 034mroF1 is no longer available'
    );
  });

  it('tags the stderr lines', () => {
    h.logs[`${SOURCE}/early/stdout`] = { content: 'ok\n', totalLines: 1 };
    h.logs[`${SOURCE}/early/stderr`] = { content: 'warn\n', totalLines: 1 };
    renderCached();
    const warn = lines().find((line) => line.textContent?.includes('warn'))!;
    expect(within(warn).getByText('ERR')).toBeVisible();
  });
});
