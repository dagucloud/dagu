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
import { ResumeBackfillDialog } from '../ResumeBackfillDialog';

type Backfill = components['schemas']['Backfill'];
type BackfillPlan = components['schemas']['BackfillPlan'];

const post = vi.hoisted(() => vi.fn());
vi.mock('@/hooks/api', () => ({ useClient: () => ({ POST: post }) }));

const BACKFILL: Backfill = {
  id: 'bf123456',
  dag: 'etl',
  status: BackfillStatus.failed,
  submittedAt: '2026-11-01T00:00:00Z',
  rerun: BackfillRerun.none,
  maxConcurrency: 1,
  onFailure: BackfillOnFailure.stop,
  slotCount: 3,
  counts: { succeeded: 1, failed: 1, 'not run': 1 },
};

function plan(revision: string, runs: number): BackfillPlan {
  return {
    dag: 'etl',
    rerun: BackfillRerun.failed,
    runCounts: { none: runs, failed: runs, all: runs },
    concurrency: 1,
    queueLimit: 1,
    revision,
    slots: Array.from({ length: runs }, (_, i) => ({
      slot: `2026-10-0${i + 1}T02:00:00Z`,
      windowEnd: `2026-10-0${i + 1}T02:00:00Z`,
      action: BackfillPlanSlotAction.run,
    })),
    warnings: ['Live runs may wait behind an executing backfill run.'],
  };
}

function renderDialog(onResumed = vi.fn()) {
  render(
    <ResumeBackfillDialog
      backfill={BACKFILL}
      remoteNode="local"
      open
      onOpenChange={vi.fn()}
      onResumed={onResumed}
    />
  );
  return onResumed;
}

describe('ResumeBackfillDialog', () => {
  beforeEach(() => post.mockReset());

  it('shows the plan, then resumes with its revision', async () => {
    post
      .mockResolvedValueOnce({ data: { plan: plan('r1', 2) } })
      .mockResolvedValueOnce({
        data: {
          plan: plan('r1', 2),
          backfill: { ...BACKFILL, status: BackfillStatus.running },
        },
      });
    const onResumed = renderDialog();

    expect(screen.getByRole('button', { name: 'Resume' })).toBeDisabled();
    expect(await screen.findByText('Runs 2 slots')).toBeInTheDocument();
    expect(
      screen.getByText('Live runs may wait behind an executing backfill run.')
    ).toBeInTheDocument();
    expect(post).toHaveBeenLastCalledWith('/backfills/{backfillId}/resume', {
      params: {
        path: { backfillId: 'bf123456' },
        query: { remoteNode: 'local' },
      },
      body: {
        maxConcurrency: 1,
        onFailure: BackfillOnFailure.stop,
        dryRun: true,
      },
    });

    fireEvent.click(screen.getByRole('button', { name: 'Resume 2 runs' }));
    await waitFor(() => expect(onResumed).toHaveBeenCalled());
    expect(post).toHaveBeenLastCalledWith('/backfills/{backfillId}/resume', {
      params: {
        path: { backfillId: 'bf123456' },
        query: { remoteNode: 'local' },
      },
      body: {
        maxConcurrency: 1,
        onFailure: BackfillOnFailure.stop,
        revision: 'r1',
      },
    });
  });

  it('shows a plan that changed instead of resuming', async () => {
    post
      .mockResolvedValueOnce({ data: { plan: plan('r1', 2) } })
      .mockResolvedValueOnce({
        error: { plan: plan('r2', 3) },
        response: { status: 409 },
      });
    const onResumed = renderDialog();

    fireEvent.click(
      await screen.findByRole('button', { name: 'Resume 2 runs' })
    );
    expect(
      await screen.findByText('The plan changed. Review it and resume again.')
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Resume 3 runs' })).toBeEnabled();
    expect(onResumed).not.toHaveBeenCalled();
  });
});
