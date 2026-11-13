// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  BackfillOnFailure,
  BackfillPlanSlotAction,
  BackfillRerun,
  BackfillStatus,
  type components,
} from '@/api/v1/schema';
import {
  type BackfillTarget,
  StartBackfillDialog,
} from '../StartBackfillDialog';

type BackfillPlan = components['schemas']['BackfillPlan'];

const post = vi.hoisted(() => vi.fn());
vi.mock('@/hooks/api', () => ({ useClient: () => ({ POST: post }) }));

function plan(revision: string, runs: number): BackfillPlan {
  return {
    dag: 'etl',
    rerun: BackfillRerun.none,
    runCounts: { none: 2, failed: 3, all: 5 },
    concurrency: 1,
    queueLimit: 1,
    revision,
    slots: Array.from({ length: runs }, (_, i) => ({
      slot: `2026-10-0${i + 1}T02:00:00Z`,
      windowEnd: `2026-10-0${i + 1}T02:00:00Z`,
      action: BackfillPlanSlotAction.run,
    })),
  };
}

function renderDialog(target: BackfillTarget, onStarted = vi.fn()) {
  render(
    <StartBackfillDialog
      fileName="etl"
      remoteNode="local"
      target={target}
      open
      onOpenChange={vi.fn()}
      onStarted={onStarted}
    />
  );
  return onStarted;
}

const lastBody = () =>
  (post.mock.calls[post.mock.calls.length - 1]?.[1] as { body: unknown }).body;

describe('StartBackfillDialog', () => {
  beforeEach(() => {
    post.mockReset();
  });

  it('offers what to run with counts, and starts the plan shown', async () => {
    post.mockImplementation(
      async (_path: string, { body }: { body: { dryRun: boolean } }) =>
        body.dryRun
          ? { data: { plan: plan('r1', 2) } }
          : {
              data: {
                plan: plan('r1', 2),
                backfill: { id: 'bf123456', status: BackfillStatus.running },
              },
            }
    );
    const onStarted = renderDialog({
      kind: 'range',
      from: '2026-10-01T00:00:00Z',
    });

    expect(await screen.findByText('Runs 2 slots')).toBeInTheDocument();
    expect(
      screen.getByRole('radio', { name: /^Fill gaps\s*2$/ })
    ).toBeChecked();
    expect(lastBody()).toMatchObject({
      from: '2026-10-01T00:00:00Z',
      rerun: BackfillRerun.none,
      maxConcurrency: 1,
      onFailure: BackfillOnFailure.stop,
      dryRun: true,
    });

    fireEvent.click(screen.getByRole('button', { name: 'Start 2 runs' }));
    await waitFor(() =>
      expect(onStarted).toHaveBeenCalledWith(
        expect.objectContaining({ id: 'bf123456' }),
        false
      )
    );
    expect(lastBody()).toMatchObject({ revision: 'r1', dryRun: false });
  });

  it('asks before running slots that succeeded again', async () => {
    post.mockResolvedValue({ data: { plan: plan('r1', 5) } });
    renderDialog({ kind: 'range', from: '2026-10-01T00:00:00Z' });

    fireEvent.click(
      await screen.findByRole('radio', { name: /Redo everything/ })
    );
    const confirm = await screen.findByRole('checkbox', {
      name: 'Run 2 slots that succeeded again',
    });
    expect(screen.getByRole('button', { name: 'Start 5 runs' })).toBeDisabled();
    fireEvent.click(confirm);
    expect(screen.getByRole('button', { name: 'Start 5 runs' })).toBeEnabled();
  });

  it('runs picked slots as they are, without a choice of what to run', async () => {
    post.mockResolvedValue({ data: { plan: plan('r1', 2) } });
    renderDialog({
      kind: 'slots',
      slots: ['2026-10-01T02:00:00Z', '2026-10-02T02:00:00Z'],
    });

    expect(await screen.findByText('Runs 2 slots')).toBeInTheDocument();
    expect(screen.queryByRole('radio', { name: /Fill gaps/ })).toBeNull();
    expect(lastBody()).toMatchObject({
      slots: ['2026-10-01T02:00:00Z', '2026-10-02T02:00:00Z'],
    });
    expect(lastBody()).not.toHaveProperty('rerun');
  });

  it('starts only with the options its plan was made with', async () => {
    let answer: (value: unknown) => void = () => {};
    post
      .mockResolvedValueOnce({ data: { plan: plan('r1', 2) } })
      .mockImplementationOnce(
        () => new Promise((resolve) => (answer = resolve))
      );
    renderDialog({ kind: 'range', from: '2026-10-01T00:00:00Z' });
    await screen.findByText('Runs 2 slots');

    fireEvent.click(
      screen.getByRole('radio', { name: /Fill gaps and redo failed/ })
    );
    expect(screen.getByRole('button', { name: 'Start 2 runs' })).toBeDisabled();

    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    answer({ data: { plan: plan('r2', 3) } });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Start 3 runs' })).toBeEnabled()
    );
  });
});
