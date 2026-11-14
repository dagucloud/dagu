// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { Status, type components } from '@/api/v1/schema';
import { shownRun } from '../ShownRun';

type DAGFile = components['schemas']['DAGFile'];

function dagFile(
  own: { status: Status; startedAt: string },
  sub: { status: Status; startedAt: string }
): DAGFile {
  return {
    latestDAGRun: { dagRunId: 'own', ...own },
    latestSubDAGRun: {
      run: { dagRunId: 'sub', ...sub },
      rootDAGRunName: 'parent',
      rootDAGRunId: 'parent-run',
      parentDAGRunName: 'parent',
      parentDAGRunId: 'parent-run',
    },
  } as never;
}

const early = '2026-11-14T10:00:00Z';
const late = '2026-11-14T10:05:00Z';

describe('shownRun', () => {
  it('shows the later run when both have finished', () => {
    const { run, sub } = shownRun(
      dagFile(
        { status: Status.Success, startedAt: early },
        { status: Status.Failed, startedAt: late }
      )
    );
    expect(run.dagRunId).toBe('sub');
    expect(sub).toBeDefined();
  });

  // A later sub-run that already finished must not hide the DAG's own run
  // that is still going.
  it('keeps an own run that is still running', () => {
    const { run, sub } = shownRun(
      dagFile(
        { status: Status.Running, startedAt: early },
        { status: Status.Success, startedAt: late }
      )
    );
    expect(run.dagRunId).toBe('own');
    expect(sub).toBeUndefined();
  });

  // dayjs reads a missing time as now, which would hide every sub-run.
  it('shows a sub-run of a DAG that never ran on its own', () => {
    const { run } = shownRun(
      dagFile(
        { status: Status.NotStarted, startedAt: '' },
        { status: Status.Success, startedAt: early }
      )
    );
    expect(run.dagRunId).toBe('sub');
  });

  it('shows a sub-run that is still running', () => {
    const { run } = shownRun(
      dagFile(
        { status: Status.Failed, startedAt: late },
        { status: Status.Running, startedAt: early }
      )
    );
    expect(run.dagRunId).toBe('sub');
  });
});
