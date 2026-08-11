// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  components,
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import {
  buildTimelineRows,
  getSubRunQueryContext,
  hasTimelineSubRuns,
} from '../timelineItems';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type Node = components['schemas']['Node'];
type SubDAGRun = components['schemas']['SubDAGRun'];
type SubDAGRunDetail = components['schemas']['SubDAGRunDetail'];

const nowMs = Date.parse('2026-01-01T00:02:00Z');

function node(overrides: Partial<Node> = {}): Node {
  const { step: stepOverrides, ...nodeOverrides } = overrides;

  return {
    stdout: '',
    stderr: '',
    startedAt: '2026-01-01T00:00:00Z',
    finishedAt: '2026-01-01T00:00:10Z',
    status: NodeStatus.Success,
    statusLabel: NodeStatusLabel.succeeded,
    retryCount: 0,
    doneCount: 1,
    ...nodeOverrides,
    step: {
      name: 'step-a',
      ...stepOverrides,
    },
  };
}

function dagRun(overrides: Partial<DAGRunDetails> = {}): DAGRunDetails {
  return {
    dagRunId: 'root-run',
    name: 'root-dag',
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    autoRetryCount: 0,
    startedAt: '2026-01-01T00:00:00Z',
    finishedAt: '2026-01-01T00:01:00Z',
    artifactsAvailable: false,
    rootDAGRunName: 'root-dag',
    rootDAGRunId: 'root-run',
    log: '',
    nodes: [node()],
    ...overrides,
  };
}

function subRun(
  dagRunId: string,
  overrides: Partial<SubDAGRun> = {}
): SubDAGRun {
  return {
    dagRunId,
    dagName: 'child-dag',
    params: `{"ITEM":"${dagRunId}"}`,
    ...overrides,
  };
}

function subRunDetail(
  dagRunId: string,
  overrides: Partial<SubDAGRunDetail> = {}
): SubDAGRunDetail {
  return {
    dagRunId,
    dagName: 'child-dag',
    params: `{"ITEM":"${dagRunId}"}`,
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    startedAt: '2026-01-01T00:00:01Z',
    finishedAt: '2026-01-01T00:00:05Z',
    ...overrides,
  };
}

describe('timelineItems', () => {
  it('builds parent-only rows when there are no eligible child rows', () => {
    const rows = buildTimelineRows({
      dagRun: dagRun(),
      subRunDetails: [],
      nowMs,
    });

    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({
      kind: 'step',
      label: 'step-a',
      status: NodeStatus.Success,
      statusSource: 'node',
      depth: 0,
    });
  });

  it('sorts parent rows by start time', () => {
    const rows = buildTimelineRows({
      dagRun: dagRun({
        nodes: [
          node({
            step: { name: 'later' },
            startedAt: '2026-01-01T00:00:20Z',
            finishedAt: '2026-01-01T00:00:30Z',
          }),
          node({
            step: { name: 'earlier' },
            startedAt: '2026-01-01T00:00:01Z',
            finishedAt: '2026-01-01T00:00:03Z',
          }),
        ],
      }),
      subRunDetails: [],
      nowMs,
    });

    expect(rows.map((row) => row.label)).toEqual(['earlier', 'later']);
  });

  it('does not fetch or build child rows for non-parallel sub-DAG calls', () => {
    const run = dagRun({
      nodes: [
        node({
          step: { name: 'call-child', call: 'child-dag' },
          subRuns: [subRun('child-run-1')],
        }),
      ],
    });

    expect(hasTimelineSubRuns(run)).toBe(false);
    expect(
      buildTimelineRows({
        dagRun: run,
        subRunDetails: [subRunDetail('child-run-1')],
        nowMs,
      }).map((row) => row.kind)
    ).toEqual(['step']);
  });

  it('expands repeat-policy children in archive-then-current order with numbered labels', () => {
    const run = dagRun({
      nodes: [
        node({
          step: { name: 'repeat-child', call: 'child-dag' },
          subRunsRepeated: [subRun('repeat-run-1'), subRun('repeat-run-2')],
          subRuns: [subRun('repeat-run-3')],
        }),
      ],
    });

    expect(hasTimelineSubRuns(run)).toBe(true);

    const rows = buildTimelineRows({
      dagRun: run,
      subRunDetails: [
        subRunDetail('repeat-run-1', {
          startedAt: '2026-01-01T00:00:01Z',
          finishedAt: '2026-01-01T00:00:03Z',
          status: Status.Failed,
          statusLabel: StatusLabel.failed,
        }),
        subRunDetail('repeat-run-2', {
          startedAt: '2026-01-01T00:00:04Z',
          finishedAt: '2026-01-01T00:00:06Z',
        }),
        subRunDetail('repeat-run-3', {
          startedAt: '2026-01-01T00:00:07Z',
          finishedAt: '2026-01-01T00:00:09Z',
          status: Status.Running,
          statusLabel: StatusLabel.running,
        }),
      ],
      nowMs,
    });

    expect(rows.map((row) => [row.kind, row.label, row.dagRunId])).toEqual([
      ['step', 'repeat-child', undefined],
      ['subdag', '#01', 'repeat-run-1'],
      ['subdag', '#02', 'repeat-run-2'],
      ['subdag', '#03', 'repeat-run-3'],
    ]);
    expect(rows[1]).toMatchObject({
      kind: 'subdag',
      startMs: Date.parse('2026-01-01T00:00:01Z'),
      endMs: Date.parse('2026-01-01T00:00:03Z'),
      status: Status.Failed,
      statusSource: 'dagrun',
      dagName: 'child-dag',
      parentStepName: 'repeat-child',
      depth: 1,
    });
    expect(rows[3]).toMatchObject({
      kind: 'subdag',
      startMs: Date.parse('2026-01-01T00:00:07Z'),
      endMs: Date.parse('2026-01-01T00:00:09Z'),
      status: Status.Running,
      dagRunId: 'repeat-run-3',
    });
  });

  it('keeps parallel expansion on subRuns only even when subRunsRepeated is present', () => {
    const run = dagRun({
      nodes: [
        node({
          step: {
            name: 'parallel-call',
            call: 'child-dag',
            parallel: { items: ['a', 'b'] },
          },
          subRuns: [subRun('parallel-run-1'), subRun('parallel-run-2')],
          // Archived parallel batches must not appear on the timeline.
          subRunsRepeated: [subRun('archived-batch-1'), subRun('archived-batch-2')],
        }),
      ],
    });

    expect(hasTimelineSubRuns(run)).toBe(true);

    const rows = buildTimelineRows({
      dagRun: run,
      subRunDetails: [
        subRunDetail('archived-batch-1'),
        subRunDetail('archived-batch-2'),
        subRunDetail('parallel-run-1'),
        subRunDetail('parallel-run-2'),
      ],
      nowMs,
    });

    expect(rows.map((row) => [row.kind, row.label, row.dagRunId])).toEqual([
      ['step', 'parallel-call', undefined],
      ['subdag', '#01', 'parallel-run-1'],
      ['subdag', '#02', 'parallel-run-2'],
    ]);
  });

  it('retains ordinary and parallel behavior alongside repeat expansion in one DAG run', () => {
    const run = dagRun({
      nodes: [
        node({
          step: { name: 'ordinary-call', call: 'child-dag' },
          startedAt: '2026-01-01T00:00:00Z',
          finishedAt: '2026-01-01T00:00:05Z',
          subRuns: [subRun('ordinary-run')],
        }),
        node({
          step: {
            name: 'parallel-call',
            call: 'child-dag',
            parallel: { items: ['a', 'b'] },
          },
          startedAt: '2026-01-01T00:00:10Z',
          finishedAt: '2026-01-01T00:00:20Z',
          subRuns: [subRun('parallel-run-1'), subRun('parallel-run-2')],
        }),
        node({
          step: { name: 'repeat-child', call: 'child-dag' },
          startedAt: '2026-01-01T00:00:30Z',
          finishedAt: '2026-01-01T00:00:50Z',
          subRunsRepeated: [subRun('repeat-run-1')],
          subRuns: [subRun('repeat-run-2')],
        }),
      ],
    });

    expect(hasTimelineSubRuns(run)).toBe(true);

    const rows = buildTimelineRows({
      dagRun: run,
      subRunDetails: [
        subRunDetail('ordinary-run'),
        subRunDetail('parallel-run-1'),
        subRunDetail('parallel-run-2'),
        subRunDetail('repeat-run-1'),
        subRunDetail('repeat-run-2'),
      ],
      nowMs,
    });

    expect(rows.map((row) => [row.kind, row.label, row.dagRunId])).toEqual([
      ['step', 'ordinary-call', undefined],
      ['step', 'parallel-call', undefined],
      ['subdag', '#01', 'parallel-run-1'],
      ['subdag', '#02', 'parallel-run-2'],
      ['step', 'repeat-child', undefined],
      ['subdag', '#01', 'repeat-run-1'],
      ['subdag', '#02', 'repeat-run-2'],
    ]);
  });

  it('omits repeat child rows when matching timing details are missing', () => {
    const run = dagRun({
      nodes: [
        node({
          step: { name: 'repeat-child', call: 'child-dag' },
          subRunsRepeated: [subRun('repeat-run-1')],
          subRuns: [subRun('repeat-run-2')],
        }),
      ],
    });

    const rows = buildTimelineRows({
      dagRun: run,
      subRunDetails: [subRunDetail('repeat-run-2')],
      nowMs,
    });

    expect(rows.map((row) => [row.kind, row.label, row.dagRunId])).toEqual([
      ['step', 'repeat-child', undefined],
      ['subdag', '#02', 'repeat-run-2'],
    ]);
  });

  it('builds parallel child rows only when matching timing details exist', () => {
    const run = dagRun({
      nodes: [
        node({
          step: {
            name: 'parallel-call',
            call: 'child-dag',
            parallel: { items: ['a', 'b'] },
          },
          subRuns: [subRun('child-run-1'), subRun('child-run-2')],
        }),
      ],
    });

    expect(hasTimelineSubRuns(run)).toBe(true);

    const rows = buildTimelineRows({
      dagRun: run,
      subRunDetails: [subRunDetail('child-run-2')],
      nowMs,
    });

    expect(rows.map((row) => [row.kind, row.label, row.dagRunId])).toEqual([
      ['step', 'parallel-call', undefined],
      ['subdag', '#02', 'child-run-2'],
    ]);
  });

  it('filters endpoint details to each parallel node sub-run list', () => {
    const run = dagRun({
      nodes: [
        node({
          step: {
            name: 'parallel-a',
            call: 'child-a',
            parallel: { items: ['a'] },
          },
          startedAt: '2026-01-01T00:00:00Z',
          finishedAt: '2026-01-01T00:00:10Z',
          subRuns: [subRun('a-run')],
        }),
        node({
          step: {
            name: 'parallel-b',
            call: 'child-b',
            parallel: { items: ['b'] },
          },
          startedAt: '2026-01-01T00:00:20Z',
          finishedAt: '2026-01-01T00:00:30Z',
          subRuns: [subRun('b-run')],
        }),
      ],
    });

    const rows = buildTimelineRows({
      dagRun: run,
      subRunDetails: [
        subRunDetail('b-run', { dagName: 'child-b' }),
        subRunDetail('a-run', { dagName: 'child-a' }),
      ],
      nowMs,
    });

    expect(
      rows.map((row) => ({
        label: row.label,
        parentStepName: row.parentStepName,
        dagRunId: row.dagRunId,
        dagName: row.dagName,
      }))
    ).toEqual([
      {
        label: 'parallel-a',
        parentStepName: undefined,
        dagRunId: undefined,
        dagName: undefined,
      },
      {
        label: '#01',
        parentStepName: 'parallel-a',
        dagRunId: 'a-run',
        dagName: 'child-a',
      },
      {
        label: 'parallel-b',
        parentStepName: undefined,
        dagRunId: undefined,
        dagName: undefined,
      },
      {
        label: '#01',
        parentStepName: 'parallel-b',
        dagRunId: 'b-run',
        dagName: 'child-b',
      },
    ]);
  });

  it('ignores unrelated endpoint details', () => {
    const run = dagRun({
      nodes: [
        node({
          step: {
            name: 'parallel-call',
            call: 'child-dag',
            parallel: { items: ['a'] },
          },
          subRuns: [subRun('child-run-1')],
        }),
      ],
    });

    const rows = buildTimelineRows({
      dagRun: run,
      subRunDetails: [subRunDetail('other-run')],
      nowMs,
    });

    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ label: 'parallel-call' });
  });

  it('preserves parent node errors on the step row', () => {
    const rows = buildTimelineRows({
      dagRun: dagRun({
        nodes: [node({ error: 'step failed' })],
      }),
      subRunDetails: [],
      nowMs,
    });

    expect(rows[0]).toMatchObject({ error: 'step failed' });
  });

  it('returns root query context without parentSubDAGRunId for root DAG-runs', () => {
    expect(getSubRunQueryContext(dagRun())).toEqual({
      rootDagName: 'root-dag',
      rootDagRunId: 'root-run',
      parentSubDAGRunId: undefined,
    });
  });

  it('returns nested query context with the current sub-DAG run ID', () => {
    expect(
      getSubRunQueryContext(
        dagRun({
          name: 'child-dag',
          dagRunId: 'child-run',
          rootDAGRunName: 'root-dag',
          rootDAGRunId: 'root-run',
        })
      )
    ).toEqual({
      rootDagName: 'root-dag',
      rootDagRunId: 'root-run',
      parentSubDAGRunId: 'child-run',
    });
  });
});
