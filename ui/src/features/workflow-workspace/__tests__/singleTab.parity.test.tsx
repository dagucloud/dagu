// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Proof for the parity rows of the Executions pane that the single tab
// replaced: the run history now lives in the timeline of the Run panel.

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Status, TriggerType } from '@/api/v1/schema';
import { RunTimelineView } from '../dock/RunTimeline';
import { useRunHistory } from '../data/useRunHistory';
import { dagPage, idleSSE, run } from '../data/__tests__/fixtures';
import {
  renderWorkspace,
  serve,
  type FakeServer,
} from '../data/__tests__/harness';

const h = vi.hoisted(() => ({
  GET: vi.fn(),
  historySSE: vi.fn(),
}));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return {
    useQuery: createQueryHook({ GET: h.GET } as never, '/'),
    useClient: () => ({ GET: h.GET }),
  };
});
vi.mock('@/hooks/useDAGRunSSE', () => ({ useDAGRunSSE: () => idleSSE }));
vi.mock('@/hooks/useDAGHistorySSE', () => ({
  useDAGHistorySSE: h.historySSE,
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

let server: FakeServer;

beforeEach(() => {
  h.GET.mockReset();
  h.historySSE.mockReset();
  h.historySSE.mockReturnValue(idleSSE);
  server = {
    dag: dagPage(run('run-1')),
    runs: {},
    history: { dagRuns: [run('run-1'), run('run-0')], gridData: [] },
    subRuns: [],
  };
  h.GET.mockImplementation(serve(server));
});

// parity:history-load parity:common-run-list-hooks
describe('the run history of the single tab', () => {
  it('loads once the Run panel asks for it and follows the history stream', async () => {
    const view = renderWorkspace(() => useRunHistory({ enabled: true }));
    await waitFor(() =>
      expect(view.result.current?.value.runs).toHaveLength(2)
    );

    h.historySSE.mockReturnValue({
      ...idleSSE,
      isConnected: true,
      data: { dagRuns: [run('run-2')], gridData: [] },
    });
    view.rerender();
    await waitFor(() =>
      expect(view.result.current.value.runs.map((r) => r.dagRunId)).toEqual([
        'run-2',
      ])
    );
  });
});

const NOW = Date.parse('2026-03-02T12:00:00Z');
// Oldest first, as the view takes them.
const RUNS = [
  run('r1', [], Status.Success, {
    startedAt: '2026-03-01T10:00:00Z',
    finishedAt: '2026-03-01T10:00:10Z',
  }),
  run('r2', [], Status.Failed, {
    startedAt: '2026-03-01T11:00:00Z',
    finishedAt: '2026-03-01T11:00:40Z',
    triggerType: TriggerType.manual,
  }),
  run('r3', [], Status.Success, {
    startedAt: '2026-03-02T10:00:00Z',
    finishedAt: '2026-03-02T10:00:02Z',
  }),
];

function renderTimeline(onSelect = vi.fn()) {
  render(
    <MemoryRouter>
      <RunTimelineView
        runs={RUNS}
        loading={false}
        selectedId="r3"
        count={30}
        onSelect={onSelect}
        onCountChange={vi.fn()}
        now={NOW}
      />
    </MemoryRouter>
  );
  return onSelect;
}

const bars = () =>
  within(screen.getByRole('listbox', { name: 'Runs' })).getAllByRole('option');

// The History grid is the run timeline: one bar for each run, oldest on the
// left, and a click only sets the shared selection.
// parity:history-grid parity:crit-history-grid-order-and-cap parity:dup-2
describe('the run timeline that replaced the History grid', () => {
  it('draws one bar for each run, oldest first, and selects a run on click', async () => {
    const user = userEvent.setup();
    const onSelect = renderTimeline();
    expect(bars().map((b) => b.getAttribute('data-run-id'))).toEqual([
      'r1',
      'r2',
      'r3',
    ]);
    await user.click(bars()[1]!);
    expect(onSelect).toHaveBeenCalledWith('r2');
  });
});

// parity:common-trigger-labels
describe('the trigger of a run', () => {
  it('shows the trigger label in the tooltip of the bar', async () => {
    const user = userEvent.setup();
    renderTimeline();
    await user.hover(bars()[1]!);
    const tooltip = (
      await screen.findAllByTestId('ww-run-timeline-tooltip')
    )[0]!;
    expect(tooltip).toHaveTextContent('Manual');
  });
});
