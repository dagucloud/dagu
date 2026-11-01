// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  DAGRunConditionStatus,
  NodeStatus,
  NodeStatusLabel,
  ReusedInputVia,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import type { StepOutputs } from '@/features/visual-editor/contracts';
import {
  resetRunAvailability,
  RunLinksContext,
} from '@/features/visual-editor/references/RunLinks';
import { outputCache } from '@/features/visual-editor/run/outputCache';
import dayjs from '@/lib/dayjs';
import type { ApiDAGRunDetails, WorkspaceRunValue } from '../../data/types';
import { node, run as runDetails } from '../../data/__tests__/fixtures';
import { RunDetailsButton } from '../RunDetailsPopover';

const h = vi.hoisted(() => ({
  state: { run: null as unknown as WorkspaceRunValue },
  setTab: vi.fn(),
  select: vi.fn(),
  linkSelect: vi.fn(),
  outputs: {} as Record<string, { data?: StepOutputs }>,
}));

vi.mock('../../data', () => ({ useWorkspaceRun: () => h.state.run }));
vi.mock('../../data/context', () => ({
  useWorkspaceData: () => ({ fileName: 'etl.yaml' }),
}));
vi.mock('../../dock/DockContext', () => ({
  useDock: () => ({ setTab: h.setTab }),
  useDockCommands: () => ({ setTab: h.setTab }),
}));
vi.mock('@/features/visual-editor/api/useStepOutputs', () => ({
  useStepOutputs: (_dag: string, _runId: string | null, step: string | null) =>
    h.outputs[step ?? ''] ?? { data: undefined },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

function makeRun(
  details: ApiDAGRunDetails,
  over: Partial<WorkspaceRunValue> = {}
): WorkspaceRunValue {
  return {
    selection: 'latest',
    ref: { name: 'demo', runId: details.dagRunId },
    details,
    hasRun: true,
    isLatest: true,
    isActive: false,
    select: h.select,
    ...over,
  } as unknown as WorkspaceRunValue;
}

function details(extra: Partial<ApiDAGRunDetails> = {}): ApiDAGRunDetails {
  return runDetails(
    'run-42',
    [
      node('a'),
      node('b', NodeStatus.Failed, { statusLabel: NodeStatusLabel.failed }),
    ],
    Status.Success,
    {
      startedAt: '2026-03-13T10:00:30Z',
      finishedAt: '2026-03-13T10:02:30Z',
      ...extra,
    }
  );
}

function renderButton(
  ui: ApiDAGRunDetails,
  over: Partial<WorkspaceRunValue> = {},
  wrap: (el: ReactElement) => ReactElement = (el) => el
) {
  h.state.run = makeRun(ui, over);
  return render(wrap(<RunDetailsButton details={ui} />));
}

async function open() {
  await userEvent.click(screen.getByRole('button', { name: 'Run details' }));
  return screen.getByRole('dialog', { name: 'Run details' });
}

beforeEach(() => {
  h.setTab.mockClear();
  h.select.mockClear();
  h.linkSelect.mockClear();
  h.outputs = {};
  resetRunAvailability();
  outputCache.clear();
});

describe('RunDetailsPopover', () => {
  // The popover is on demand: closed, nothing of the details is in the DOM.
  // parity:chg-run-details-header
  it('is closed until the button is used, then shows the status of a finished run', async () => {
    renderButton(details());
    const button = screen.getByRole('button', { name: 'Run details' });
    expect(button).toHaveAttribute('aria-haspopup', 'dialog');
    expect(button).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('dialog')).toBeNull();

    const dialog = await open();
    expect(button).toHaveAttribute('aria-expanded', 'true');
    expect(button).toHaveAttribute('aria-controls', dialog.id);
    expect(
      within(dialog).getByTestId('ww-run-details-status')
    ).toHaveTextContent('Succeeded');
    // A finished run with nothing to add shows only its status line.
    expect(within(dialog).queryByRole('progressbar')).toBeNull();
    expect(within(dialog).queryByTestId('ww-run-details-error')).toBeNull();
    expect(within(dialog).queryByTestId('ww-run-details-test')).toBeNull();
    expect(within(dialog).queryByTestId('ww-run-details-reused')).toBeNull();
    expect(within(dialog).queryByTestId('runtime-conditions')).toBeNull();
    expect(within(dialog).queryByTestId('ww-run-params')).toBeNull();
  });

  it('keeps the slot of the button at 28px, whatever the run', () => {
    renderButton(details());
    expect(screen.getByTestId('ww-run-details-slot')).toHaveClass(
      'size-7',
      'shrink-0'
    );
  });

  it('shows the error of a failed run in monospace, and Show log opens the Log tab', async () => {
    renderButton(
      details({
        status: Status.Failed,
        statusLabel: StatusLabel.failed,
        error: 'exit status 1',
      })
    );
    const dialog = await open();
    expect(
      within(dialog).getByTestId('ww-run-details-status')
    ).toHaveTextContent('Failed');
    const error = within(dialog).getByTestId('ww-run-details-error');
    expect(error).toHaveTextContent('exit status 1');
    expect(error).toHaveClass('font-mono', 'text-destructive', 'break-words');

    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Show log' })
    );
    expect(h.setTab).toHaveBeenCalledWith('log', { user: true });
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  // parity:overview-progress-bar parity:overview-scheduler-log-button
  // parity:overview-execution-status-message
  it('shows the status message, the steps done and the log button of an active run', async () => {
    renderButton(
      details({ status: Status.Running, statusLabel: StatusLabel.running }),
      { isActive: true }
    );
    const dialog = await open();
    expect(
      within(dialog).getByText('Execution in progress')
    ).toBeInTheDocument();
    const bar = within(dialog).getByRole('progressbar', {
      name: 'Run progress',
    });
    expect(bar).toHaveAttribute('aria-valuenow', '1');
    expect(bar).toHaveAttribute('aria-valuemax', '2');
    expect(bar).toHaveTextContent('1/2');
    // The failed step has ended, but only the succeeded one counts.
    expect(bar).toHaveAttribute('aria-valuetext', '1 of 2 succeeded');
    expect(within(bar).getByTitle('Succeeded: 1')).toBeInTheDocument();

    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Show log' })
    );
    expect(h.setTab).toHaveBeenCalledTimes(1);
  });

  it('shows no progress for a finished run', async () => {
    renderButton(details(), { isActive: false });
    const dialog = await open();
    expect(within(dialog).queryByRole('progressbar')).toBeNull();
  });

  it('says when a test run disappears, and falls back to the delay before its end', async () => {
    const { unmount } = renderButton(
      details({ ephemeral: true, finishedAt: '2026-03-13T10:02:30Z' })
    );
    let dialog = await open();
    const at = dayjs(Date.parse('2026-03-13T10:02:30Z') + 10 * 60_000).format(
      'HH:mm:ss'
    );
    expect(within(dialog).getByTestId('ww-run-details-test')).toHaveTextContent(
      `Test run, not kept: it disappears at ${at}`
    );
    unmount();

    renderButton(details({ ephemeral: true, finishedAt: '-' }));
    dialog = await open();
    expect(within(dialog).getByTestId('ww-run-details-test')).toHaveTextContent(
      'Test run, not kept: it disappears 10 minutes after it ends'
    );
  });

  describe('a run with reused inputs', () => {
    const reused = {
      reusedInputs: [
        {
          step: 'a',
          runId: 'run-7',
          startedAt: '2026-03-13T09:00:00Z',
          finishedAt: '2026-03-13T09:00:20Z',
          via: ReusedInputVia.run,
        },
        {
          step: 'b',
          runId: 'run-7',
          startedAt: '2026-03-13T09:00:20Z',
          finishedAt: '2026-03-13T09:00:25Z',
          via: ReusedInputVia.run,
        },
      ],
      nodes: [node('a', NodeStatus.Skipped), node('b', NodeStatus.Skipped)],
    } satisfies Partial<ApiDAGRunDetails>;

    function withLinks(held: string[]) {
      const links = {
        select: h.linkSelect,
        resolve: (id: string) =>
          held.find((r) => r.toLowerCase() === id.toLowerCase()) ?? null,
        exists: async () => false,
      };
      return (el: ReactElement) => (
        <RunLinksContext.Provider value={links}>{el}</RunLinksContext.Provider>
      );
    }

    it('shows the reused time, the source run with its link, and the inputs folded', async () => {
      h.outputs['a'] = {
        data: {
          stepName: 'a',
          status: NodeStatus.Skipped,
          statusLabel: NodeStatusLabel.skipped,
          outputs: [
            { name: 'rows', value: '42', truncated: false, size: 2 },
            { name: 'note', value: 'second value', truncated: false, size: 12 },
          ],
        },
      };
      renderButton(
        details({
          ...reused,
          labels: ['dagu.source=editor', 'dagu.outputs-from=abc1'],
        }),
        {},
        withLinks(['AbC1'])
      );
      const dialog = await open();
      expect(
        within(dialog).getByTestId('ww-run-reused-time')
      ).toHaveTextContent('Reused time: 25s');
      // The case of the ID comes from the history; the line shows its short form.
      const from = within(dialog).getByTestId('ww-run-inputs-from');
      expect(from).toHaveTextContent('Inputs from run AbC1');
      await userEvent.click(
        within(from).getByRole('button', { name: 'Show run' })
      );
      expect(h.select).toHaveBeenCalledWith('AbC1');
      expect(screen.queryByRole('dialog')).toBeNull();

      await open();
      // The list is folded behind its chevron.
      expect(screen.queryByTestId('ww-run-inputs-used')).toBeNull();
      const fold = screen.getByRole('button', { name: 'Inputs used (2)' });
      expect(fold).toHaveAttribute('aria-expanded', 'false');
      await userEvent.click(fold);
      expect(fold).toHaveAttribute('aria-expanded', 'true');
      const list = screen.getByTestId('ww-run-inputs-used');
      expect(within(list).getByTestId('inputs-used-a')).toBeInTheDocument();
      expect(within(list).getByTestId('inputs-used-b')).toBeInTheDocument();
      // The values are behind their own toggle.
      expect(screen.queryByText('second value')).toBeNull();
      await userEvent.click(screen.getByTestId('inputs-used-toggle-a'));
      expect(screen.getByText('second value')).toBeInTheDocument();
    });

    // A source run that the history does not hold: the record of the reused
    // inputs gives the case of its ID, not the lower-case label.
    it('takes the case of the source run ID from the reused inputs', async () => {
      renderButton(
        details({
          ...reused,
          reusedInputs: reused.reusedInputs.map((input) => ({
            ...input,
            runId: 'AbC1',
          })),
          labels: ['dagu.outputs-from=abc1'],
        }),
        {},
        withLinks([])
      );
      const dialog = await open();
      const from = within(dialog).getByTestId('ww-run-inputs-from');
      expect(from).toHaveTextContent('Inputs from run AbC1');
      await userEvent.click(
        within(from).getByRole('button', { name: 'Show run' })
      );
      expect(h.select).toHaveBeenCalledWith('AbC1');
    });

    it('shows nothing of the reused block for a run that reused no step', async () => {
      renderButton(details());
      const dialog = await open();
      expect(within(dialog).queryByText(/Reused time/)).toBeNull();
      expect(within(dialog).queryByTestId('ww-run-inputs-from')).toBeNull();
      expect(
        within(dialog).queryByRole('button', { name: /Inputs used/ })
      ).toBeNull();
    });
  });

  // parity:overview-run-error parity:overview-runtime-conditions
  // parity:overview-run-preconditions
  it('shows the run error, the runtime conditions and the unmet preconditions', async () => {
    renderButton(
      details({
        status: Status.Failed,
        statusLabel: StatusLabel.failed,
        error: 'exit status 1',
        conditions: [
          {
            type: 'Runnable',
            status: DAGRunConditionStatus.False,
            reason: 'NoWorker',
            message: 'No worker matches the selector',
            checkedAt: '2026-03-13T10:00:10Z',
          },
        ],
        preconditions: [
          { condition: '`date +%u`', expected: '1', error: 'got 3' },
        ],
      })
    );
    const dialog = await open();
    expect(
      within(dialog).getByTestId('ww-run-details-error')
    ).toHaveTextContent('exit status 1');
    expect(within(dialog).getByTestId('runtime-conditions')).toHaveTextContent(
      'No worker matches the selector'
    );
    expect(
      within(dialog).getByText('Run precondition not met')
    ).toBeInTheDocument();
    expect(within(dialog).getByText(/got 3/)).toBeInTheDocument();
  });

  // parity:header-run-params
  it('shows the parameters of the run folded, once, in a mono box of at most 120px', async () => {
    renderButton(details({ params: 'region=eu n=3' }));
    const dialog = await open();
    expect(within(dialog).queryByTestId('ww-run-params')).toBeNull();
    const fold = within(dialog).getByRole('button', { name: 'Parameters' });
    expect(fold).toHaveAttribute('aria-expanded', 'false');
    await userEvent.click(fold);

    const box = within(dialog).getByTestId('ww-run-params');
    expect(box).toHaveTextContent('region=eu n=3');
    expect(box).toHaveClass('font-mono', 'max-h-[120px]', 'overflow-y-auto');
    expect(within(dialog).getAllByText('region=eu n=3')).toHaveLength(1);
  });

  it('shows no parameters row for a run without parameters', async () => {
    renderButton(details({ params: '' }));
    const dialog = await open();
    expect(
      within(dialog).queryByRole('button', { name: 'Parameters' })
    ).toBeNull();
  });

  // parity:history-status-overview
  it('gives the status line the colour of its tone', async () => {
    renderButton(details({ status: Status.Aborted }));
    const dialog = await open();
    const status = within(dialog).getByTestId('ww-run-details-status');
    expect(within(status).getByText('Aborted')).toHaveClass(
      'text-[var(--status-aborted)]'
    );
  });

  describe('keyboard and pointer', () => {
    it('opens with Enter, moves the focus into the dialog, and closes with Escape with the focus back on the button', async () => {
      renderButton(details());
      const button = screen.getByRole('button', { name: 'Run details' });
      await userEvent.tab();
      expect(button).toHaveFocus();
      await userEvent.keyboard('{Enter}');
      const dialog = screen.getByRole('dialog', { name: 'Run details' });
      await waitFor(() => expect(dialog).toHaveFocus());

      await userEvent.keyboard('{Escape}');
      expect(screen.queryByRole('dialog')).toBeNull();
      expect(button).toHaveFocus();
      expect(button).toHaveAttribute('aria-expanded', 'false');
    });

    it('closes on a click outside, and keeps open on a click inside', async () => {
      render(
        <div>
          <span data-testid="outside">outside</span>
        </div>
      );
      renderButton(details());
      const dialog = await open();
      await userEvent.click(
        within(dialog).getByTestId('ww-run-details-status')
      );
      expect(screen.getByRole('dialog')).toBeInTheDocument();
      await userEvent.click(screen.getByTestId('outside'));
      expect(screen.queryByRole('dialog')).toBeNull();
    });

    it('limits the panel to 24rem wide and 70vh high, with its own scroll', async () => {
      renderButton(details());
      const dialog = await open();
      expect(dialog).toHaveClass('max-h-[70vh]', 'overflow-y-auto');
      expect(dialog).toHaveClass('w-[min(24rem,calc(100vw-1rem))]');
    });

    // The owner does not want a tooltip that repeats what is visible.
    it('shows the tooltip on keyboard focus only, not on hover', async () => {
      renderButton(details());
      const button = screen.getByRole('button', { name: 'Run details' });
      await userEvent.hover(button);
      await new Promise((resolve) => setTimeout(resolve, 50));
      expect(screen.queryByRole('tooltip')).toBeNull();
      await userEvent.unhover(button);

      await userEvent.tab();
      expect(await screen.findByRole('tooltip')).toHaveTextContent(
        'Run details'
      );
    });
  });
});
