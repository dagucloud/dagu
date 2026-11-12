// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigationType,
} from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status, TriggerType } from '@/api/v1/schema';
import type { ApiDAGDetails } from '../../contracts';
import { node, run as runDetails } from '../../data/__tests__/fixtures';
import { nodesByStep, NO_WAITING } from '../../data/runFacts';
import type { ApiDAGRunDetails, WorkspaceRunValue } from '../../data/types';
import { RunChip } from '../RunChip';

const { state, useRunHistoryMock, openStep, staleMock, diffMock } = vi.hoisted(
  () => ({
    state: { run: null as unknown as WorkspaceRunValue },
    useRunHistoryMock: vi.fn(),
    openStep: vi.fn(),
    staleMock: vi.fn(),
    diffMock: vi.fn(),
  })
);

// The arrows read the visible runs of the timeline, which read the history.
vi.mock('../../data', () => ({ useWorkspaceRun: () => state.run }));
vi.mock('../../data/useWorkspaceRun', () => ({
  useWorkspaceRun: () => state.run,
}));
vi.mock('../../data/useRunHistory', () => ({
  useRunHistory: useRunHistoryMock,
}));
vi.mock('../../dock/DockContext', () => ({
  useDock: () => ({ openStep }),
  useDockCommands: () => ({ openStep }),
}));
vi.mock('@/features/visual-editor/run/useRunSpecStale', () => ({
  useRunSpecStale: staleMock,
  useRunSpecDiff: diffMock,
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const history = {
  runs: [] as ApiDAGRunDetails[],
  isLoading: false,
};

// jsdom has no layout: the width of the bar around the chip is a variable.
// The row of facts and the tooltip of the status read it (useFactTiers).
const bar = { width: 1920 };
const realRect = HTMLElement.prototype.getBoundingClientRect;

function makeRun(
  details: ApiDAGRunDetails | null,
  over: Partial<WorkspaceRunValue> = {}
): WorkspaceRunValue {
  const runId = details?.dagRunId ?? null;
  return {
    selection: 'latest',
    ref: runId === null ? null : { name: 'demo', runId },
    details,
    hasRun: runId !== null,
    isLatest: true,
    isActive: false,
    notFound: false,
    latestRef:
      details === null
        ? null
        : { runId: details.dagRunId, status: details.status },
    overlay: {},
    nodeByStep: nodesByStep(details),
    handlers: [],
    waiting: NO_WAITING,
    failedStep: null,
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn(async () => {}),
    ...over,
  };
}

function Probe() {
  const location = useLocation();
  return (
    <div
      data-testid="loc"
      data-nav={useNavigationType()}
      data-url={`${location.pathname}${location.search}`}
    />
  );
}

const dag = { name: 'demo' } as ApiDAGDetails;

function chip() {
  return (
    <div data-testid="ww-bar">
      <RunChip fileName="demo" dag={dag} workspaceName={null} />
    </div>
  );
}

function renderChip(url = '/dags/demo') {
  const view = render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route
          path="/dags/:fileName/:tab?"
          element={
            <>
              {chip()}
              <Probe />
            </>
          }
        />
      </Routes>
    </MemoryRouter>
  );
  const loc = () => screen.getByTestId('loc');
  return { ...view, loc };
}

beforeEach(() => {
  window.localStorage.clear();
  bar.width = 1920;
  HTMLElement.prototype.getBoundingClientRect = function () {
    const width = this.dataset.testid === 'ww-bar' ? bar.width : 0;
    return {
      width,
      height: 0,
      top: 0,
      left: 0,
      right: width,
      bottom: 0,
    } as DOMRect;
  };
  openStep.mockReset();
  staleMock.mockReset();
  staleMock.mockReturnValue(false);
  diffMock.mockReset();
  diffMock.mockReturnValue(null);
  history.runs = [];
  history.isLoading = false;
  useRunHistoryMock.mockReset();
  useRunHistoryMock.mockImplementation(({ enabled }: { enabled: boolean }) =>
    enabled
      ? { runs: history.runs, gridData: [], isLoading: history.isLoading }
      : { runs: [], gridData: [], isLoading: false }
  );
  state.run = makeRun(runDetails('r3'));
});

afterEach(() => {
  HTMLElement.prototype.getBoundingClientRect = realRect;
});

// parity:header-status-icon parity:run-header-status parity:dup-8
describe('RunChip status', () => {
  it('shows the status icon with its tooltip and accessible name', () => {
    state.run = makeRun(runDetails('r3', [node('a')], Status.Failed));
    renderChip();
    const icon = screen.getByRole('img', { name: 'Failed' });
    expect(icon).toHaveAttribute('title', 'Failed');
  });

  // The icon takes its colour from lib/statusPalette.ts. parity:dup-23
  it('colours the icon of an aborted run with the aborted token', () => {
    state.run = makeRun(runDetails('r3', [node('a')], Status.Aborted));
    renderChip();
    const icon = screen.getByRole('img', { name: 'Aborted' });
    expect(icon.style.color).toBe('var(--status-aborted)');
  });

  it('announces a status change in a polite live region', () => {
    state.run = makeRun(runDetails('r3', [node('a')], Status.Running), {
      isActive: true,
    });
    const { rerender } = renderChip();
    const live = screen.getByRole('status');
    expect(live).toHaveAttribute('aria-live', 'polite');
    expect(live).toHaveTextContent('Running');

    state.run = makeRun(runDetails('r3', [node('a')], Status.Failed));
    rerender(
      <MemoryRouter initialEntries={['/dags/demo']}>
        <Routes>
          <Route
            path="/dags/:fileName/:tab?"
            element={
              <>
                {chip()}
                <Probe />
              </>
            }
          />
        </Routes>
      </MemoryRouter>
    );
    expect(screen.getByRole('status')).toBe(live);
    expect(live).toHaveTextContent('Failed');
  });

  // parity:header-last-run-group
  it('names the group Last run while it shows the latest run', () => {
    renderChip();
    expect(screen.getByRole('group', { name: 'Last run' })).toBeVisible();
  });

  it('names the group Selected run for an older pinned run', () => {
    state.run = makeRun(runDetails('r2'), {
      selection: { runId: 'r2' },
      isLatest: false,
    });
    renderChip();
    expect(screen.getByRole('group', { name: 'Selected run' })).toBeVisible();
  });
});

// parity:header-auto-retry-badge parity:embed-workflow-header-meta
describe('RunChip auto-retry badge', () => {
  it('shows the auto retries of a failed run with a limit', () => {
    state.run = makeRun(
      runDetails('r3', [node('a')], Status.Failed, {
        autoRetryCount: 1,
        autoRetryLimit: 3,
      })
    );
    renderChip();
    expect(screen.getByText('1/3 auto retries')).toBeInTheDocument();
  });

  it('hides the badge for a run that did not fail', () => {
    state.run = makeRun(
      runDetails('r3', [node('a')], Status.Success, {
        autoRetryCount: 1,
        autoRetryLimit: 3,
      })
    );
    renderChip();
    expect(screen.queryByText(/auto retries/)).toBeNull();
  });

  it('hides the badge without a limit', () => {
    state.run = makeRun(
      runDetails('r3', [node('a')], Status.Failed, { autoRetryLimit: 0 })
    );
    renderChip();
    expect(screen.queryByText(/auto retr/)).toBeNull();
  });
});

// parity:overview-failed-step-banner parity:run-failure-banner
// parity:test-run-header-meta
describe('RunChip failed step', () => {
  it('opens the stderr of the failed step and writes ?step with replace', async () => {
    const details = runDetails(
      'r3',
      [node('a'), node('b', NodeStatus.Failed, { error: 'boom' })],
      Status.Failed
    );
    state.run = makeRun(details, { failedStep: 'b' });
    const { loc } = renderChip();
    const failed = screen.getByRole('button', { name: 'Failed at b' });
    // The label is cut to fit the bar: the title has the whole text.
    expect(failed).toHaveAttribute('title', 'Failed at b: boom');
    await userEvent.click(failed);
    expect(loc()).toHaveAttribute('data-url', '/dags/demo?step=b');
    expect(loc()).toHaveAttribute('data-nav', 'REPLACE');
    expect(openStep).toHaveBeenCalledWith({ step: 'b', stream: 'stderr' });
  });

  // The error of the run is the reason that the header of Executions gave.
  it('prefers the error of the run in the title of the failed step', () => {
    const details = runDetails(
      'r3',
      [node('b', NodeStatus.Failed, { error: 'step error' })],
      Status.Failed,
      { error: 'precondition not met' } as Partial<ApiDAGRunDetails>
    );
    state.run = makeRun(details, { failedStep: 'b' });
    renderChip();
    expect(screen.getByRole('button', { name: 'Failed at b' })).toHaveAttribute(
      'title',
      'Failed at b: precondition not met'
    );
  });

  it('says Rejected at for a rejected step', async () => {
    const details = runDetails(
      'r3',
      [node('gate', NodeStatus.Rejected)],
      Status.Rejected
    );
    state.run = makeRun(details, { failedStep: 'gate' });
    const { loc } = renderChip();
    await userEvent.click(
      screen.getByRole('button', { name: 'Rejected at gate' })
    );
    expect(loc()).toHaveAttribute('data-url', '/dags/demo?step=gate');
    expect(openStep).toHaveBeenCalledWith({ step: 'gate', stream: 'stderr' });
  });

  // The bar slot keeps the smallest width of the chip, which holds the whole
  // label: without a cap a long step name pushed Start out of the bar.
  it('caps the width of the failed step', () => {
    const details = runDetails(
      'r3',
      [node('build-and-push-the-image', NodeStatus.Failed)],
      Status.Failed
    );
    state.run = makeRun(details, { failedStep: 'build-and-push-the-image' });
    renderChip();
    const failed = screen.getByRole('button', {
      name: 'Failed at build-and-push-the-image',
    });
    expect(failed.className).toMatch(/(^|\s)max-w-\d+(\s|$)/);
    expect(
      within(failed).getByText('Failed at build-and-push-the-image')
    ).toHaveClass('truncate');
  });

  // The run header and the list show a partial success in amber: the step
  // that failed in it is not red.
  it('shows the failed step of a partial success in the warning tone', () => {
    const details = runDetails(
      'r3',
      [node('flaky', NodeStatus.Failed, { error: 'boom' })],
      Status.PartialSuccess
    );
    state.run = makeRun(details, { failedStep: 'flaky' });
    renderChip();
    const failed = screen.getByRole('button', { name: 'Failed at flaky' });
    expect(failed).toHaveClass('text-[var(--status-warning)]');
    expect(failed).not.toHaveClass('text-[var(--status-error)]');
  });

  it('shows the failed step of a failed run in the error tone', () => {
    const details = runDetails(
      'r3',
      [node('flaky', NodeStatus.Failed)],
      Status.Failed
    );
    state.run = makeRun(details, { failedStep: 'flaky' });
    renderChip();
    expect(screen.getByRole('button', { name: 'Failed at flaky' })).toHaveClass(
      'text-[var(--status-error)]'
    );
  });

  it('shows no failure without a failed step', () => {
    renderChip();
    expect(screen.queryByText(/Failed at|Rejected at/)).toBeNull();
  });
});

// parity:header-run-meta-line parity:header-run-id-copy parity:test-run-header-meta
// parity:run-meta-line
describe('RunChip facts', () => {
  it('shows the progress while the run is active', () => {
    const details = runDetails(
      'r3',
      [
        node('a'),
        node('b', NodeStatus.Running),
        node('c', NodeStatus.NotStarted),
      ],
      Status.Running
    );
    state.run = makeRun(details, { isActive: true });
    renderChip();
    const progress = screen.getByRole('group', { name: 'Run progress' });
    expect(progress).toHaveTextContent('1/3');
    expect(progress).toHaveTextContent('1 of 3 done');
  });

  it('keeps the progress when the run is done: it is the result', () => {
    renderChip();
    expect(screen.getByLabelText('Run progress')).toBeVisible();
  });

  it('shows no progress for a run with no step', () => {
    state.run = makeRun(runDetails('r3', []));
    renderChip();
    expect(screen.queryByLabelText('Run progress')).toBeNull();
  });

  const manual = {
    triggerType: TriggerType.manual,
    startedAt: '2026-01-15T02:00:00Z',
    finishedAt: '2026-01-15T02:00:05Z',
  } as Partial<ApiDAGRunDetails>;

  // The row of facts shows only where the bar has room: the facts go in the
  // order trigger, start, duration, and the whole ID is the last.
  it('shows the whole row of facts from a bar of 1280px', () => {
    state.run = makeRun(
      runDetails(
        'run-0123456789abcdefghij',
        [node('a')],
        Status.Success,
        manual
      )
    );
    bar.width = 1280;
    renderChip();
    const row = screen.getByTestId('ww-run-facts');
    expect(within(row).getByTitle('Trigger')).toHaveTextContent('Manual');
    expect(within(row).getByTitle('Started')).toBeInTheDocument();
    expect(within(row).getByTitle('Duration')).toHaveTextContent('5s');
    // The whole ID, with no title that would repeat it.
    const id = within(row).getByText('run-0123456789abcdefghij');
    expect(id).not.toHaveAttribute('title');
    expect(
      within(row).getByRole('button', { name: 'Copy run ID' })
    ).toBeInTheDocument();
  });

  it.each([
    [1279, ['Started', 'Duration']],
    [1216, ['Started', 'Duration']],
  ])('drops the trigger first, at a bar of %ipx', (width, titles) => {
    state.run = makeRun(runDetails('r3', [node('a')], Status.Success, manual));
    bar.width = width;
    renderChip();
    const row = screen.getByTestId('ww-run-facts');
    expect(within(row).queryByTitle('Trigger')).toBeNull();
    for (const title of titles) {
      expect(within(row).getByTitle(title)).toBeInTheDocument();
    }
    expect(within(row).getByText('r3')).toBeInTheDocument();
  });

  it('drops the start before the duration, at a bar of 1152px', () => {
    state.run = makeRun(runDetails('r3', [node('a')], Status.Success, manual));
    bar.width = 1152;
    renderChip();
    const row = screen.getByTestId('ww-run-facts');
    expect(within(row).queryByTitle('Trigger')).toBeNull();
    expect(within(row).queryByTitle('Started')).toBeNull();
    expect(within(row).getByTitle('Duration')).toBeInTheDocument();
    expect(within(row).getByText('r3')).toBeInTheDocument();
  });

  it('shows no row under 1152px', () => {
    state.run = makeRun(runDetails('r3', [node('a')], Status.Success, manual));
    bar.width = 1151;
    renderChip();
    expect(screen.queryByTestId('ww-run-facts')).toBeNull();
  });

  it('puts the failed step in the place of the trigger in the row', () => {
    state.run = makeRun(
      runDetails('r3', [node('b', NodeStatus.Failed)], Status.Failed, manual),
      { failedStep: 'b' }
    );
    renderChip();
    const row = screen.getByTestId('ww-run-facts');
    expect(within(row).queryByTitle('Trigger')).toBeNull();
    // One failed step control, which comes right before the row.
    const failed = screen.getAllByRole('button', { name: 'Failed at b' });
    expect(failed).toHaveLength(1);
    expect(failed[0]).toHaveClass('order-1', 'text-[var(--status-error)]');
    expect(row).toHaveClass('order-1');
    expect(
      failed[0]!.compareDocumentPosition(row) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  // The tooltip of the status says only what the bar does not show.
  it('has no tooltip when the row shows everything and nothing is extra', async () => {
    state.run = makeRun(runDetails('r3', [node('a')], Status.Success, manual));
    renderChip();
    const identity = screen.getByTestId('ww-run-chip-identity');
    expect(identity).not.toHaveAttribute('tabindex');
    await userEvent.hover(identity);
    expect(screen.queryByRole('tooltip')).toBeNull();
    // The status is still named, by the title of its icon.
    expect(screen.getByRole('img', { name: 'Succeeded' })).toHaveAttribute(
      'title',
      'Succeeded'
    );
  });

  // The local worker is the default: it is no reason for a tooltip.
  it('has no tooltip for a run of the local worker', async () => {
    state.run = makeRun(
      runDetails('r3', [node('a')], Status.Success, {
        ...manual,
        workerId: 'local',
      })
    );
    renderChip();
    await userEvent.hover(screen.getByTestId('ww-run-chip-identity'));
    expect(screen.queryByRole('tooltip')).toBeNull();
  });

  it('gives only the worker and the profile when the row shows every fact', async () => {
    state.run = makeRun(
      runDetails('r3', [node('a')], Status.Success, {
        ...manual,
        workerId: 'worker-7',
        profileName: 'fast',
      })
    );
    renderChip();
    await userEvent.hover(screen.getByTestId('ww-run-chip-identity'));
    const tip = await screen.findByRole('tooltip');
    expect(within(tip).getByText('worker-7')).toBeInTheDocument();
    expect(within(tip).getByText('fast')).toBeInTheDocument();
    // Shown in the row: not repeated.
    expect(within(tip).queryByText('r3')).toBeNull();
    expect(within(tip).queryByTitle('Duration')).toBeNull();
    expect(within(tip).queryByTitle('Started')).toBeNull();
    expect(within(tip).queryByTitle('Trigger')).toBeNull();
  });

  it('adds the facts that the bar has no room for', async () => {
    state.run = makeRun(runDetails('r3', [node('a')], Status.Success, manual));
    bar.width = 1200;
    renderChip();
    await userEvent.hover(screen.getByTestId('ww-run-chip-identity'));
    const tip = await screen.findByRole('tooltip');
    // The trigger is out of the row at 1200px.
    expect(within(tip).getByText('Manual')).toBeInTheDocument();
    expect(within(tip).queryByText('r3')).toBeNull();
    expect(within(tip).queryByTitle('Duration')).toBeNull();
  });

  it('gives the run ID and every fact in the tooltip under 1152px', async () => {
    state.run = makeRun(runDetails('r3', [node('a')], Status.Success, manual));
    bar.width = 1000;
    renderChip();
    await userEvent.hover(screen.getByTestId('ww-run-chip-identity'));
    const tip = await screen.findByRole('tooltip');
    expect(within(tip).getByText('r3')).toBeInTheDocument();
    expect(within(tip).getByText('Manual')).toBeInTheDocument();
    expect(within(tip).getByTitle('Duration')).toHaveTextContent('5s');
    // The start shows as an absolute time: no relative text is on screen.
    expect(within(tip).getByTitle('Started')).toHaveTextContent('2026-01-15');
  });

  it('is a keyboard target of the tooltip when it has something to say', () => {
    bar.width = 1000;
    renderChip();
    expect(screen.getByTestId('ww-run-chip-identity')).toHaveAttribute(
      'tabindex',
      '0'
    );
  });

  it('copies the run ID from the tooltip', async () => {
    const user = userEvent.setup();
    bar.width = 1000;
    renderChip();
    await user.hover(screen.getByTestId('ww-run-chip-identity'));
    const tip = await screen.findByRole('tooltip');
    await user.click(within(tip).getByRole('button', { name: 'Copy run ID' }));
    await waitFor(async () =>
      expect(await navigator.clipboard.readText()).toBe('r3')
    );
  });
});

// parity:chg-run-url-latest
describe('RunChip healthy', () => {
  const repeating = (status: NodeStatus, doneCount = 3) =>
    node('heartbeat', status, {
      doneCount,
      step: { repeatPolicy: { repeat: 'while', interval: 10 } },
    });

  it('reads Healthy for a run that never ends, with the repeat count and no 0/1', () => {
    state.run = makeRun(
      runDetails('r3', [repeating(NodeStatus.Running)], Status.Running),
      { isActive: true }
    );
    renderChip();
    expect(screen.getByRole('img', { name: 'Healthy' })).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('Healthy');
    expect(screen.getByTestId('ww-run-chip')).toHaveAttribute(
      'data-status',
      String(Status.Running)
    );
    expect(screen.getByTestId('repeat-badge')).toHaveTextContent('×3');
    expect(screen.queryByRole('group', { name: 'Run progress' })).toBeNull();
    expect(screen.queryByText('0/1')).toBeNull();
  });

  it('shows no count before the continuous step has run', () => {
    state.run = makeRun(
      runDetails('r3', [repeating(NodeStatus.Running, 0)], Status.Running),
      { isActive: true }
    );
    renderChip();
    expect(screen.getByRole('img', { name: 'Healthy' })).toBeInTheDocument();
    expect(screen.queryByTestId('repeat-badge')).toBeNull();
    expect(screen.queryByRole('group', { name: 'Run progress' })).toBeNull();
  });

  it('keeps Running and the progress for an ordinary run', () => {
    state.run = makeRun(
      runDetails('r3', [node('a', NodeStatus.Running)], Status.Running),
      { isActive: true }
    );
    renderChip();
    expect(screen.getByRole('img', { name: 'Running' })).toBeInTheDocument();
    expect(
      screen.getByRole('group', { name: 'Run progress' })
    ).toHaveTextContent('0/1');
  });

  it('keeps the failure visible when a node of the run failed', () => {
    state.run = makeRun(
      runDetails(
        'r3',
        [repeating(NodeStatus.Running), node('other', NodeStatus.Failed)],
        Status.Running
      ),
      { isActive: true, failedStep: 'other' }
    );
    renderChip();
    expect(screen.queryByRole('img', { name: 'Healthy' })).toBeNull();
    expect(screen.getByRole('img', { name: 'Running' })).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Failed at other' })
    ).toBeVisible();
  });
});

describe('RunChip navigation', () => {
  it('loads no history before an arrow is used', () => {
    renderChip();
    expect(useRunHistoryMock).toHaveBeenCalled();
    expect(
      useRunHistoryMock.mock.calls.every(([o]) => o.enabled === false)
    ).toBe(true);
  });

  // The server sends the runs newest first.
  it('selects the older and the newer run with push', async () => {
    history.runs = [runDetails('r3'), runDetails('r2'), runDetails('r1')];
    state.run = makeRun(runDetails('r2'), {
      selection: { runId: 'r2' },
      isLatest: false,
    });
    renderChip();
    await userEvent.click(screen.getByRole('button', { name: 'Older run' }));
    expect(useRunHistoryMock).toHaveBeenLastCalledWith({
      enabled: true,
      includeEphemeral: true,
      pollTestRuns: false,
    });
    expect(state.run.select).toHaveBeenCalledWith('r1');
    await userEvent.click(screen.getByRole('button', { name: 'Newer run' }));
    expect(state.run.select).toHaveBeenLastCalledWith('r3');
  });

  it('selects the neighbour once the history has loaded', async () => {
    history.runs = [runDetails('r3'), runDetails('r2'), runDetails('r1')];
    history.isLoading = true;
    state.run = makeRun(runDetails('r2'), {
      selection: { runId: 'r2' },
      isLatest: false,
    });
    const { rerender } = renderChip();
    await userEvent.click(screen.getByRole('button', { name: 'Older run' }));
    expect(state.run.select).not.toHaveBeenCalled();
    history.isLoading = false;
    rerender(
      <MemoryRouter initialEntries={['/dags/demo']}>
        <Routes>
          <Route path="/dags/:fileName/:tab?" element={chip()} />
        </Routes>
      </MemoryRouter>
    );
    expect(state.run.select).toHaveBeenCalledWith('r1');
  });

  it('disables Newer run on the latest run and Older run on the first', async () => {
    history.runs = [runDetails('r3')];
    renderChip();
    // Both wait for the history: a newer test run can exist.
    const newer = screen.getByRole('button', { name: 'Newer run' });
    expect(newer).toBeEnabled();
    const older = screen.getByRole('button', { name: 'Older run' });
    expect(older).toBeEnabled();
    await userEvent.click(older);
    expect(older).toBeDisabled();
    expect(newer).toBeDisabled();
    expect(newer).toHaveAttribute('title', 'No newer run in this view');
    expect(state.run.select).not.toHaveBeenCalled();
  });

  it('disables Older run on the oldest run of the history', async () => {
    history.runs = [runDetails('r3'), runDetails('r2'), runDetails('r1')];
    state.run = makeRun(runDetails('r1'), {
      selection: { runId: 'r1' },
      isLatest: false,
    });
    renderChip();
    const older = screen.getByRole('button', { name: 'Older run' });
    await userEvent.click(older);
    expect(older).toBeDisabled();
    expect(state.run.select).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole('button', { name: 'Newer run' }));
    expect(state.run.select).toHaveBeenCalledWith('r2');
  });

  // The Latest button is an arrow with a bar: it is the last of the arrows,
  // and it is disabled while the chip already shows the latest run.
  it('disables the latest button while it follows the latest run', () => {
    renderChip();
    expect(screen.getByRole('button', { name: 'Latest run' })).toBeDisabled();
    expect(screen.queryByText('Latest')).toBeNull();
    expect(screen.queryByText('Back to latest')).toBeNull();
  });

  it('disables the latest button for a run in the URL that is the latest', () => {
    state.run = makeRun(runDetails('r3'), {
      selection: { runId: 'r3' },
      isLatest: true,
    });
    renderChip();
    expect(screen.getByRole('button', { name: 'Latest run' })).toBeDisabled();
  });

  // A test run is not a run of the workflow: the chip says "Test run" with
  // a flask, and still goes back.
  it('says Test run with a flask for a test run', async () => {
    state.run = makeRun(
      runDetails('t1', [], Status.Success, { ephemeral: true }),
      {
        selection: { runId: 't1' },
        isLatest: false,
      }
    );
    renderChip();
    expect(screen.queryByText('Latest')).toBeNull();
    expect(screen.queryByText('Back to latest')).toBeNull();
    const test = screen.getByTestId('ww-run-test-chip');
    expect(test).toHaveTextContent('Test run');
    expect(test.querySelector('svg.lucide-flask-conical')).not.toBeNull();
    await userEvent.click(test);
    expect(state.run.select).toHaveBeenCalledWith('latest');
  });

  // The latest button goes to the latest run from an older run, also when
  // a newer run appeared while this one was shown.
  it('goes to the latest run from an older run', async () => {
    state.run = makeRun(runDetails('r2'), {
      selection: { runId: 'r2' },
      isLatest: false,
    });
    renderChip();
    const latest = screen.getByRole('button', { name: 'Latest run' });
    expect(latest).toBeEnabled();
    expect(latest.querySelector('svg.lucide-chevron-last')).not.toBeNull();
    await userEvent.click(latest);
    expect(state.run.select).toHaveBeenCalledWith('latest');
  });
});

// parity:test-ve-run
describe('RunChip spec-differs dot', () => {
  const plain = 'This run used a different definition.';
  const some =
    'This run used a different definition. 2 steps of the run are not on the canvas.';
  const one =
    'This run used a different definition. 1 step of the run is not on the canvas.';
  const diff = (removed: string[]) => ({
    headChanged: false,
    changed: new Set<string>(),
    removedFromLive: new Set(removed),
    addedInLive: new Set<string>(),
  });

  it('asks about the selected run', () => {
    renderChip();
    expect(staleMock).toHaveBeenCalledWith({
      dagName: 'demo',
      fileName: 'demo',
      runId: 'r3',
    });
    expect(screen.queryByTestId('ww-run-stale-dot')).toBeNull();
  });

  // The dot toggles the run definition view: it is a pressed button while
  // ?def=run is set, and a click writes the key with push.
  it('writes ?def=run with push and is not pressed before', async () => {
    staleMock.mockReturnValue(true);
    diffMock.mockReturnValue(diff(['a', 'b']));
    const { loc } = renderChip();
    const dot = screen.getByTestId('ww-run-stale-dot');
    expect(dot).toHaveAttribute('aria-pressed', 'false');
    await userEvent.click(dot);
    expect(loc()).toHaveAttribute('data-url', '/dags/demo?def=run');
    expect(loc()).toHaveAttribute('data-nav', 'PUSH');
  });

  it('is pressed while ?def=run is set, and a click clears it with push', async () => {
    staleMock.mockReturnValue(true);
    const { loc } = renderChip('/dags/demo?def=run&step=a');
    const dot = screen.getByTestId('ww-run-stale-dot');
    expect(dot).toHaveAttribute('aria-pressed', 'true');
    await userEvent.click(dot);
    expect(loc()).toHaveAttribute('data-url', '/dags/demo?step=a');
    expect(loc()).toHaveAttribute('data-nav', 'PUSH');
    expect(screen.getByTestId('ww-run-stale-dot')).toHaveAttribute(
      'aria-pressed',
      'false'
    );
  });

  it('says how many steps of the run are not on the canvas', async () => {
    staleMock.mockReturnValue(true);
    diffMock.mockReturnValue(diff(['a', 'b']));
    renderChip();
    const dot = screen.getByRole('button', { name: some });
    await userEvent.hover(dot);
    const tip = await screen.findByRole('tooltip');
    expect(tip).toHaveTextContent(some);
    expect(tip).toHaveTextContent('Show the definition of this run');
  });

  it('uses the singular for one step', () => {
    staleMock.mockReturnValue(true);
    diffMock.mockReturnValue(diff(['a']));
    renderChip();
    expect(screen.getByRole('button', { name: one })).toBeVisible();
  });

  it('names no count when the run has no step that is off the canvas', () => {
    staleMock.mockReturnValue(true);
    diffMock.mockReturnValue(diff([]));
    renderChip();
    expect(screen.getByRole('button', { name: plain })).toBeVisible();
  });

  // Under ?def=run the canvas shows the run definition: no step of the run
  // is missing from it.
  it('names no count while ?def=run is set', () => {
    staleMock.mockReturnValue(true);
    diffMock.mockReturnValue(diff(['a', 'b']));
    renderChip('/dags/demo?def=run');
    expect(screen.getByRole('button', { name: plain })).toBeVisible();
  });

  it('offers the way back in the tooltip while ?def=run is set', async () => {
    staleMock.mockReturnValue(true);
    renderChip('/dags/demo?def=run');
    await userEvent.hover(screen.getByTestId('ww-run-stale-dot'));
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'Back to the current definition'
    );
  });
});

describe('RunChip newer run', () => {
  const text = 'A newer run is active';

  it('shows when a pinned run is not the active latest run', async () => {
    state.run = makeRun(runDetails('r2'), {
      selection: { runId: 'r2' },
      isLatest: false,
      latestRef: { runId: 'r3', status: Status.Running },
    });
    renderChip();
    await userEvent.click(screen.getByRole('button', { name: text }));
    expect(state.run.select).toHaveBeenCalledWith('latest');
  });

  // The API names an older run that is still active as the latest one.
  it('hides when the active latest run is older than the selected run', () => {
    state.run = makeRun(
      runDetails('r2', [node('a')], Status.Success, {
        startedAt: '2026-01-01T10:00:05Z',
      }),
      {
        selection: { runId: 'r2' },
        isLatest: true,
        latestRef: {
          runId: 'r1',
          status: Status.Running,
          at: Date.parse('2026-01-01T10:00:00Z'),
        },
      }
    );
    renderChip();
    expect(screen.queryByText(text)).toBeNull();
    expect(screen.queryByText('Back to latest')).toBeNull();
  });

  it('shows when the active latest run is newer than the selected run', () => {
    state.run = makeRun(
      runDetails('r1', [node('a')], Status.Success, {
        startedAt: '2026-01-01T10:00:00Z',
      }),
      {
        selection: { runId: 'r1' },
        isLatest: false,
        latestRef: {
          runId: 'r2',
          status: Status.Queued,
          at: Date.parse('2026-01-01T10:00:05Z'),
        },
      }
    );
    renderChip();
    expect(screen.getByRole('button', { name: text })).toBeInTheDocument();
  });

  it('hides when the latest run is done', () => {
    state.run = makeRun(runDetails('r2'), {
      selection: { runId: 'r2' },
      isLatest: false,
      latestRef: { runId: 'r3', status: Status.Success },
    });
    renderChip();
    expect(screen.queryByText(text)).toBeNull();
  });

  it('hides while the chip follows the latest run', () => {
    state.run = makeRun(runDetails('r3', [node('a')], Status.Running), {
      latestRef: { runId: 'r3', status: Status.Running },
    });
    renderChip();
    expect(screen.queryByText(text)).toBeNull();
  });
});

describe('RunChip empty states', () => {
  // parity:overview-loading-run
  it('says Loading run while a pinned run loads', () => {
    state.run = makeRun(null, {
      selection: { runId: 'r9' },
      ref: { name: 'demo', runId: 'r9' },
      hasRun: true,
      isLatest: false,
    });
    renderChip();
    const status = screen.getByRole('status');
    expect(within(status).getByText('Loading run...')).toBeVisible();
  });
});

// The chip keeps its width in every state: the stale dot and the count have a slot of fixed size that is there, empty, while the part
// does not show. The width is measured in the browser; here the slots say
// what keeps it.
describe('RunChip slots', () => {
  const STALE = 'ww-run-stale-slot';
  const COUNT = 'ww-run-count-slot';

  // The warning dot of a run of another definition stands in front of the run
  // ID, in place of the #: the row has the one or the other.
  it('puts the # in front of the run ID of a current run', () => {
    renderChip();
    const id = screen
      .getByTestId('ww-run-facts')
      .querySelector('[title="Run ID"]')!;
    expect(id.querySelector('svg.lucide-hash')).not.toBeNull();
    expect(screen.queryByTestId('ww-run-stale-dot')).toBeNull();
    expect(screen.queryByTestId(STALE)).toBeNull();
  });

  it('puts the dot in place of the # for a run of another definition', () => {
    staleMock.mockReturnValue(true);
    renderChip();
    const id = screen
      .getByTestId('ww-run-facts')
      .querySelector('[title="Run ID"]')!;
    expect(
      within(id as HTMLElement).getByTestId('ww-run-stale-dot')
    ).toBeVisible();
    expect(id.querySelector('svg.lucide-hash')).toBeNull();
    // The dot has no slot of its own in the row.
    expect(screen.queryByTestId(STALE)).toBeNull();
  });

  // A bar with no room for a row of facts keeps the slot of the dot, empty
  // while the run is current, so that the chip keeps its width.
  it('keeps an empty slot for the dot while there is no row of facts', () => {
    bar.width = 900;
    renderChip();
    expect(screen.queryByTestId('ww-run-facts')).toBeNull();
    const stale = screen.getByTestId(STALE);
    expect(stale).toHaveClass('size-5', 'shrink-0');
    expect(stale).toBeEmptyDOMElement();
  });

  it('puts the dot in its slot while there is no row of facts', () => {
    bar.width = 900;
    staleMock.mockReturnValue(true);
    renderChip();
    expect(
      within(screen.getByTestId(STALE)).getByTestId('ww-run-stale-dot')
    ).toBeVisible();
  });

  it('keeps an empty slot for the count', () => {
    state.run = makeRun(runDetails('r3', []));
    renderChip();
    const count = screen.getByTestId(COUNT);
    expect(count).toHaveClass('w-[8ch]', 'shrink-0');
    expect(count).toBeEmptyDOMElement();
    expect(screen.queryByTestId('ww-run-progress')).toBeNull();
  });

  it('shows the progress in the slot of the count', () => {
    const details = runDetails(
      'r3',
      [node('a'), node('b', NodeStatus.NotStarted)],
      Status.Running
    );
    state.run = makeRun(details, { isActive: true });
    renderChip();
    expect(
      screen.getByRole('group', { name: 'Run progress' })
    ).toHaveTextContent('1/2');
    const count = screen.getByTestId(COUNT);
    expect(within(count).getByTestId('ww-run-progress')).toHaveClass(
      'shrink-0',
      'tabular-nums'
    );
    expect(count).toHaveClass('w-[8ch]');
  });

  // A finished run fills the slot of the count with the steps that succeeded
  // out of the steps that had to run, in the colour of its status. A skipped
  // step is not counted.
  it('shows the done steps of a finished run, with no skipped step, in its tone', () => {
    state.run = makeRun(
      runDetails(
        'r3',
        [node('a'), node('b'), node('c', NodeStatus.Skipped)],
        Status.Success
      )
    );
    renderChip();
    const progress = within(screen.getByTestId(COUNT)).getByTestId(
      'ww-run-progress'
    );
    expect(progress).toHaveTextContent('2/2');
    expect(progress.className).toContain('--status-success');
  });

  it('shows the failed count of a failed run in the error tone', () => {
    state.run = makeRun(
      runDetails(
        'r3',
        [node('a'), node('b', NodeStatus.Failed)],
        Status.Failed
      ),
      { failedStep: 'b' }
    );
    renderChip();
    const progress = screen.getByTestId('ww-run-progress');
    expect(progress).toHaveTextContent('1/2');
    expect(progress.className).toContain('--status-error');
  });
});
