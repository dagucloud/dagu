// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  components,
  NodeStatus,
  NodeStatusLabel,
  RepeatMode,
  ReusedInputVia,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import {
  buildTimeMarkers,
  buildTimelineRows,
  getSubRunQueryContext,
  hasTimelineSubRuns,
  minBarMs,
  orderNodesForDisplay,
  placeTimelineRows,
  type TimelineRow,
} from '../timelineItems';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type Node = components['schemas']['Node'];
type SubDAGRun = components['schemas']['SubDAGRun'];
type SubDAGRunDetail = components['schemas']['SubDAGRunDetail'];

const nowMs = Date.parse('2026-01-01T00:02:00Z');
const repeatPolicy = { repeat: RepeatMode.While };

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

  it('expands the first repeat-policy child before any run is archived', () => {
    const run = dagRun({
      nodes: [
        node({
          step: { name: 'repeat-child', call: 'child-dag', repeatPolicy },
          subRuns: [subRun('repeat-run-1')],
        }),
      ],
    });

    expect(hasTimelineSubRuns(run)).toBe(true);
    expect(
      buildTimelineRows({
        dagRun: run,
        subRunDetails: [subRunDetail('repeat-run-1')],
        nowMs,
      }).map((row) => [row.kind, row.label, row.dagRunId])
    ).toEqual([
      ['step', 'repeat-child', undefined],
      ['subdag', '#01', 'repeat-run-1'],
    ]);
  });

  it('expands repeat-policy children in archive-then-current order with numbered labels', () => {
    const run = dagRun({
      nodes: [
        node({
          step: { name: 'repeat-child', call: 'child-dag', repeatPolicy },
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
          subRunsRepeated: [
            subRun('archived-batch-1'),
            subRun('archived-batch-2'),
          ],
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
          step: { name: 'repeat-child', call: 'child-dag', repeatPolicy },
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
          step: { name: 'repeat-child', call: 'child-dag', repeatPolicy },
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

  // The lifecycle handlers are in the Steps list and on the canvas: they are
  // on the timeline too, after the steps they follow.
  it('adds the lifecycle handlers after the steps, in start order', () => {
    const rows = buildTimelineRows({
      dagRun: dagRun({
        nodes: [node({ startedAt: '2026-01-01T00:00:00Z' })],
        onSuccess: node({
          step: { name: 'notify' },
          startedAt: '2026-01-01T00:00:12Z',
          finishedAt: '2026-01-01T00:00:14Z',
        }),
        onExit: node({
          step: { name: 'cleanup' },
          startedAt: '2026-01-01T00:00:14Z',
          finishedAt: '2026-01-01T00:00:15Z',
        }),
      }),
      subRunDetails: [],
      nowMs,
    });

    expect(rows.map((row) => row.id)).toEqual([
      'step:step-a',
      'handler:onSuccess',
      'handler:onExit',
    ]);
    expect(rows[1]?.label).toBe('notify');
  });

  it('leaves out a handler that did not start', () => {
    const rows = buildTimelineRows({
      dagRun: dagRun({
        onFailure: node({
          step: { name: 'alert' },
          startedAt: '-',
          status: NodeStatus.NotStarted,
        }),
      }),
      subRunDetails: [],
      nowMs,
    });

    expect(rows.map((row) => row.id)).toEqual(['step:step-a']);
  });
});

describe('buildTimeMarkers', () => {
  const start = Date.parse('2026-01-01T06:40:46.000Z');

  it('gives six labels that all differ on a span under 10 seconds', () => {
    for (const span of [1000, 1500, 4000, 9000]) {
      const labels = buildTimeMarkers(start, start + span).map((m) => m.label);
      expect(labels).toHaveLength(6);
      expect(new Set(labels).size).toBe(6);
    }
  });

  it('adds tenths when the ticks are under one second apart', () => {
    const labels = buildTimeMarkers(start, start + 3000).map((m) => m.label);
    expect(labels[1]).toMatch(/:\d\d\.\d$/);
  });

  it('keeps whole seconds when the ticks are at least one second apart', () => {
    const labels = buildTimeMarkers(start, start + 20_000).map((m) => m.label);
    expect(labels.every((l) => /^\d\d:\d\d:\d\d$/.test(l))).toBe(true);
  });

  // A span of a day or more names its dates: the axis never reads as hours of
  // one day.
  it('names the dates on a span of a day or more', () => {
    const day = 86_400_000;
    const hours = buildTimeMarkers(start, start + 2 * day).map((m) => m.label);
    expect(
      hours.every((l) => /^[A-Z][a-z]{2} \d{1,2} \d\d:\d\d$/.test(l))
    ).toBe(true);
    const days = buildTimeMarkers(start, start + 20 * day).map((m) => m.label);
    expect(days.every((l) => /^[A-Z][a-z]{2} \d{1,2}$/.test(l))).toBe(true);
    const months = buildTimeMarkers(start, start + 400 * day).map(
      (m) => m.label
    );
    expect(months.every((l) => /^[A-Z][a-z]{2} \d{4}$/.test(l))).toBe(true);
    const years = buildTimeMarkers(start, start + 7 * 365 * day).map(
      (m) => m.label
    );
    expect(years.every((l) => /^\d{4}$/.test(l))).toBe(true);
    expect(new Set(years).size).toBeGreaterThan(1);
  });

  // A gap shorter than a month or a year can put two ticks in one: 20 days
  // from Jan 1 hits March twice, 365 days from Jan 1 2028 hits 2028 twice.
  it('never repeats a month or a year label', () => {
    const day = 86_400_000;
    const months = buildTimeMarkers(start, start + 100 * day).map(
      (m) => m.label
    );
    expect(new Set(months).size).toBe(6);
    const leap = Date.parse('2028-01-01T12:00:00.000Z');
    const years = buildTimeMarkers(leap, leap + 5 * 365 * day).map(
      (m) => m.label
    );
    expect(new Set(years).size).toBe(6);
  });

  it('adds milliseconds when the ticks are under 100 ms apart', () => {
    const labels = buildTimeMarkers(start, start + 300).map((m) => m.label);
    expect(labels[0]).toMatch(/\.\d{3}$/);
    expect(new Set(labels).size).toBe(6);
  });

  // The axis of the step timeline has one text-xs mono label for each tick:
  // 7.2px for each character, 8px of room at the left and the right edge. At
  // 390px the old five gaps put six labels of 10 to 12 characters over each
  // other. A label that does not fit goes; the labels that stay never touch.
  describe('on a narrow chart', () => {
    const CHAR_PX = 7.2;
    const EDGE_PX = 8;
    const GAP_PX = 4;
    const boxes = (labels: { position: number; label: string }[], w: number) =>
      labels.map((m, i) => {
        const width = m.label.length * CHAR_PX;
        const x = (m.position / 100) * w;
        if (i === 0) return [EDGE_PX, EDGE_PX + width];
        if (i === labels.length - 1) return [w - EDGE_PX - width, w - EDGE_PX];
        return [x - width / 2, x + width / 2];
      });

    it.each([390, 320, 280, 640, 1200])(
      'never overlaps two labels at %ipx, whatever the short span',
      (width) => {
        for (const span of [150, 600, 1500, 3000, 9000, 20_000, 120_000]) {
          const markers = buildTimeMarkers(start, start + span, width);
          expect(markers.length).toBeGreaterThanOrEqual(1);
          const b = boxes(markers, width);
          for (let i = 1; i < b.length; i++) {
            expect(b[i]![0]! - b[i - 1]![1]!).toBeGreaterThanOrEqual(GAP_PX);
          }
          expect(b[0]![0]!).toBeGreaterThanOrEqual(0);
          expect(b[b.length - 1]![1]!).toBeLessThanOrEqual(width);
          expect(new Set(markers.map((m) => m.label)).size).toBe(
            markers.length
          );
        }
      }
    );

    it('drops labels at 390px and keeps the first and the last', () => {
      const markers = buildTimeMarkers(start, start + 3000, 390);
      expect(markers.length).toBeLessThan(6);
      expect(markers[0]!.position).toBe(0);
      expect(markers[markers.length - 1]!.position).toBe(100);
    });

    it('keeps the six labels on a wide chart', () => {
      expect(buildTimeMarkers(start, start + 3000, 1200)).toHaveLength(6);
    });
  });
});

// The times of a run that an older version stored have a resolution of one
// second: a fast step and its successor carry the same time. The chart draws
// the dependency order. A new run has millisecond times.
describe('placeTimelineRows', () => {
  const MIN = 10;

  function stepRow(
    name: string,
    startMs: number,
    endMs: number,
    dependsOn: string[] = []
  ): TimelineRow {
    return {
      id: `step:${name}`,
      kind: 'step',
      label: name,
      startMs,
      endMs,
      status: NodeStatus.Success,
      statusSource: 'node',
      depth: 0,
      dependsOn,
    };
  }

  function place(rows: TimelineRow[]) {
    const placed = placeTimelineRows(rows, MIN);
    const at = (name: string) => placed.find((r) => r.label === name)!;
    return { placed, at, names: placed.map((r) => r.label) };
  }

  it('draws a chain of three 0 ms steps of one second as a staircase', () => {
    const { at, names } = place([
      stepRow('c', 1000, 1000, ['b']),
      stepRow('a', 1000, 1000),
      stepRow('b', 1000, 1000, ['a']),
    ]);

    expect(names).toEqual(['a', 'b', 'c']);
    expect(at('a')).toMatchObject({ shownStartMs: 1000, shownEndMs: 1010 });
    expect(at('b')).toMatchObject({
      shownStartMs: 1010,
      shownEndMs: 1020,
      shiftedAfter: 'a',
    });
    expect(at('c')).toMatchObject({
      shownStartMs: 1020,
      shownEndMs: 1030,
      shiftedAfter: 'b',
    });
    expect(at('a').shiftedAfter).toBeUndefined();
    // The real values stay on the row.
    expect(at('c')).toMatchObject({ startMs: 1000, endMs: 1000 });
  });

  it('keeps the real millisecond times of a new run', () => {
    const base = Date.parse('2026-01-01T00:00:05.000Z');
    const { at, names } = place([
      stepRow('c', base + 40, base + 52, ['b']),
      stepRow('a', base, base + 12),
      stepRow('b', base + 20, base + 31, ['a']),
    ]);

    expect(names).toEqual(['a', 'b', 'c']);
    expect(at('a')).toMatchObject({
      shownStartMs: base,
      shownEndMs: base + 12,
    });
    expect(at('b')).toMatchObject({
      shownStartMs: base + 20,
      shownEndMs: base + 31,
    });
    expect(at('c')).toMatchObject({
      shownStartMs: base + 40,
      shownEndMs: base + 52,
    });
    expect(at('b').shiftedAfter).toBeUndefined();
    expect(at('c').shiftedAfter).toBeUndefined();
  });

  it('still pushes a successor that a new run records at the same ms', () => {
    const base = Date.parse('2026-01-01T00:00:05.250Z');
    const { at } = place([
      stepRow('a', base, base),
      stepRow('b', base, base, ['a']),
    ]);

    expect(at('b')).toMatchObject({
      shownStartMs: base + MIN,
      shiftedAfter: 'a',
    });
  });

  it('puts the two parallel 0 ms steps after one at the same place', () => {
    const { at, names } = place([
      stepRow('root', 1000, 1000),
      stepRow('left', 1000, 1000, ['root']),
      stepRow('right', 1000, 1000, ['root']),
    ]);

    expect(names).toEqual(['root', 'left', 'right']);
    expect(at('left').shownStartMs).toBe(1010);
    expect(at('right').shownStartMs).toBe(1010);
    expect(at('right').shownEndMs).toBe(1020);
  });

  it('starts the join of a diamond after the later branch', () => {
    const { at, names } = place([
      stepRow('top', 1000, 1000),
      stepRow('long', 1000, 3000, ['top']),
      stepRow('short', 1000, 1000, ['top']),
      stepRow('join', 3000, 3000, ['long', 'short']),
    ]);

    expect(at('long')).toMatchObject({ shownStartMs: 1010, shownEndMs: 3000 });
    expect(at('short')).toMatchObject({ shownStartMs: 1010, shownEndMs: 1020 });
    expect(at('join')).toMatchObject({
      shownStartMs: 3000,
      shownEndMs: 3010,
    });
    expect(names.indexOf('join')).toBe(3);
  });

  it('moves a step that starts before its dependency ends by rounding', () => {
    const { at } = place([
      stepRow('a', 1000, 3000),
      stepRow('b', 2000, 4000, ['a']),
    ]);

    expect(at('b')).toMatchObject({
      shownStartMs: 3000,
      shownEndMs: 4000,
      shiftedAfter: 'a',
    });
  });

  it('keeps the real overlap of parallel branches and a later start', () => {
    const { at } = place([
      stepRow('a', 1000, 5000),
      stepRow('b', 2000, 6000),
      stepRow('c', 7000, 8000, ['a']),
    ]);

    expect(at('b')).toMatchObject({ shownStartMs: 2000, shownEndMs: 6000 });
    expect(at('b').shiftedAfter).toBeUndefined();
    expect(at('c')).toMatchObject({ shownStartMs: 7000, shownEndMs: 8000 });
    expect(at('c').shiftedAfter).toBeUndefined();
  });

  it('ignores a dependency that has no row', () => {
    const { at } = place([stepRow('a', 1000, 1000, ['missing'])]);

    expect(at('a')).toMatchObject({ shownStartMs: 1000, shownEndMs: 1010 });
    expect(at('a').shiftedAfter).toBeUndefined();
  });

  it('does not loop on a dependency cycle', () => {
    const { placed, names } = place([
      stepRow('a', 1000, 1000, ['b']),
      stepRow('b', 1000, 1000, ['a']),
      stepRow('self', 1000, 1000, ['self']),
    ]);

    expect(placed).toHaveLength(3);
    expect(new Set(names)).toEqual(new Set(['a', 'b', 'self']));
    for (const row of placed) {
      expect(row.shownEndMs).toBeGreaterThan(row.shownStartMs);
    }
  });

  it('orders the rows by drawn start, a child row after its parent', () => {
    const child: TimelineRow = {
      ...stepRow('#01', 1000, 2000),
      id: 'subdag:a:run-1',
      kind: 'subdag',
      parentStepName: 'a',
      depth: 1,
      dependsOn: undefined,
    };
    const { placed } = place([
      stepRow('b', 1000, 1000, ['a']),
      stepRow('a', 1000, 2000),
      child,
    ]);

    expect(placed.map((r) => r.id)).toEqual([
      'step:a',
      'subdag:a:run-1',
      'step:b',
    ]);
    expect(placed[1]!.shownStartMs).toBeGreaterThanOrEqual(1000);
  });

  it('reads the dependencies of the nodes: depends, inferred and router targets', () => {
    const at = '2026-01-01T00:00:00Z';
    const done = { startedAt: at, finishedAt: at };
    const rows = buildTimelineRows({
      dagRun: dagRun({
        nodes: [
          node({ ...done, step: { name: 'z-last', depends: ['mid'] } }),
          node({ ...done, step: { name: 'mid', inferredDepends: ['route'] } }),
          node({
            ...done,
            step: {
              name: 'route',
              router: {
                value: '${X}',
                routes: [{ pattern: 'a', targets: ['hit'] }],
              },
            },
          }),
          node({ ...done, step: { name: 'hit' } }),
        ],
      }),
      subRunDetails: [],
      nowMs,
    });
    const by = Object.fromEntries(rows.map((r) => [r.label, r.dependsOn]));

    expect(by['z-last']).toEqual(['mid']);
    expect(by['mid']).toEqual(['route']);
    expect(by['hit']).toEqual(['route']);

    const placed = placeTimelineRows(rows, MIN);
    expect(placed.map((r) => r.label)).toEqual([
      'route',
      'mid',
      'hit',
      'z-last',
    ]);
  });
});

describe('minBarMs', () => {
  it('is one percent of the span or 4 px of it, whichever is larger', () => {
    expect(minBarMs(10_000, 800)).toBe(100);
    expect(minBarMs(10_000, 200)).toBe(200);
  });
});

describe('orderNodesForDisplay', () => {
  it('puts a successor under its dependency and keeps a node that did not start', () => {
    const at = '2026-01-01T00:00:00Z';
    const done = { startedAt: at, finishedAt: at };
    const nodes = [
      node({ ...done, step: { name: 'second', depends: ['first'] } }),
      node({
        step: { name: 'never' },
        status: NodeStatus.NotStarted,
        startedAt: '-',
        finishedAt: '-',
      }),
      node({ ...done, step: { name: 'first' } }),
    ];

    expect(
      orderNodesForDisplay(dagRun({ nodes }), nowMs).map((n) => n.step.name)
    ).toEqual(['first', 'never', 'second']);
  });
});

// A step that the run reused ran in another run: its row is a ghost, as long
// as it took there, drawn as if it had run just before this run, and it comes
// before its dependents without moving them.
describe('ghost rows of the steps that a run reused', () => {
  // `slow` and `middle` ran in other runs, a week ago; `use` runs now.
  const reusedRun = () =>
    dagRun({
      startedAt: '2026-01-08T10:00:00Z',
      finishedAt: '2026-01-08T10:00:05Z',
      reusedInputs: [
        {
          step: 'slow',
          runId: 'run-A',
          startedAt: '2026-01-01T00:00:00Z',
          finishedAt: '2026-01-01T00:00:20Z',
          capturedAt: '2026-01-01T00:00:20Z',
          via: ReusedInputVia.run,
        },
        {
          step: 'middle',
          runId: 'run-A',
          startedAt: '2026-01-01T00:00:20Z',
          finishedAt: '2026-01-01T00:00:22Z',
          capturedAt: '2026-01-01T00:00:22Z',
          via: ReusedInputVia.run,
        },
      ],
      nodes: [
        node({
          step: { name: 'slow' },
          status: NodeStatus.Skipped,
          startedAt: '2026-01-01T00:00:00Z',
          finishedAt: '2026-01-01T00:00:20Z',
        }),
        node({
          step: { name: 'middle', depends: ['slow'] },
          status: NodeStatus.Skipped,
          startedAt: '2026-01-01T00:00:20Z',
          finishedAt: '2026-01-01T00:00:22Z',
        }),
        node({
          step: { name: 'use', depends: ['middle'] },
          startedAt: '2026-01-08T10:00:00Z',
          finishedAt: '2026-01-08T10:00:05Z',
        }),
      ],
    });

  const rows = () =>
    buildTimelineRows({ dagRun: reusedRun(), subRunDetails: [], nowMs });

  it('makes a ghost row of each reused step, with the run and the time that it took', () => {
    const ghosts = rows().filter((row) => row.ghost);
    expect(ghosts.map((row) => row.label)).toEqual(['slow', 'middle']);
    expect(ghosts[0]?.ghost).toMatchObject({
      runId: 'run-A',
      source: 'run',
      durationMs: 20_000,
    });
    expect(ghosts[1]?.ghost?.durationMs).toBe(2_000);
    expect(rows().find((row) => row.label === 'use')?.ghost).toBeUndefined();
  });

  it('chains the ghosts before the start of the run, the last one ending at it', () => {
    const [slow, middle] = rows().filter((row) => row.ghost);
    const start = Date.parse('2026-01-08T10:00:00Z');
    expect(middle?.endMs).toBe(start);
    expect(middle?.startMs).toBe(start - 2_000);
    // The first ghost ends where the next one starts, and keeps its 20 s.
    expect(slow?.endMs).toBe(start - 2_000);
    expect(slow?.startMs).toBe(start - 22_000);
    expect(slow?.ghost?.runStartMs).toBe(start);
  });

  it('draws the ghosts first, in dependency order, and moves no step that ran', () => {
    const placed = placeTimelineRows(rows(), 100);
    expect(placed.map((row) => row.label)).toEqual(['slow', 'middle', 'use']);
    const [slow, middle, use] = placed;
    // A ghost waits for a ghost: the chain stands one after the other.
    expect(middle?.shownStartMs).toBe(slow?.shownEndMs);
    // The step that ran keeps its real time: no ghost pushes it.
    expect(use?.shownStartMs).toBe(Date.parse('2026-01-08T10:00:00Z'));
    expect(use?.shiftedAfter).toBeUndefined();
  });

  it('draws no ghost for a step whose time is unknown', () => {
    const run = reusedRun();
    run.reusedInputs = [
      { step: 'slow', runId: 'run-A', via: ReusedInputVia.cache },
    ];
    run.nodes[0] = node({
      step: { name: 'slow' },
      status: NodeStatus.Skipped,
      startedAt: '-',
      finishedAt: '-',
    });
    const built = buildTimelineRows({ dagRun: run, subRunDetails: [], nowMs });
    expect(built.some((row) => row.label === 'slow')).toBe(false);
  });

  it('makes no ghost for a step that ran', () => {
    const built = buildTimelineRows({
      dagRun: dagRun(),
      subRunDetails: [],
      nowMs,
    });
    expect(built.some((row) => row.ghost)).toBe(false);
  });

  it('keeps the ghosts first in the order of the nodes for display', () => {
    expect(
      orderNodesForDisplay(reusedRun(), nowMs).map((n) => n.step.name)
    ).toEqual(['slow', 'middle', 'use']);
  });
});

// The reused steps stand before the start of the run, chained in dependency
// order, and no step that ran is touched.
describe('ghost rows before the start of the run', () => {
  const start = Date.parse('2026-01-08T10:00:00Z');
  const reusedStep = (name: string, seconds: number) => ({
    step: name,
    runId: 'run-A',
    startedAt: '2026-01-01T00:00:00Z',
    finishedAt: new Date(
      Date.parse('2026-01-01T00:00:00Z') + seconds * 1000
    ).toISOString(),
    via: ReusedInputVia.run,
  });
  const skipped = (name: string, depends: string[] = []) =>
    node({
      step: { name, depends },
      status: NodeStatus.Skipped,
      startedAt: '2026-01-01T00:00:00Z',
      finishedAt: '2026-01-01T00:00:01Z',
    });
  const runOf = (
    reused: [string, number][],
    nodes: ReturnType<typeof node>[]
  ) =>
    dagRun({
      startedAt: '2026-01-08T10:00:00Z',
      finishedAt: '2026-01-08T10:00:05Z',
      reusedInputs: reused.map(([name, seconds]) => reusedStep(name, seconds)),
      nodes: [
        ...nodes,
        node({
          step: { name: 'use', depends: nodes.map((n) => n.step.name) },
          startedAt: '2026-01-08T10:00:00Z',
          finishedAt: '2026-01-08T10:00:05Z',
        }),
      ],
    });
  const placed = (run: ReturnType<typeof runOf>) =>
    new Map(
      placeTimelineRows(
        buildTimelineRows({ dagRun: run, subRunDetails: [], nowMs }),
        100
      ).map((row) => [row.label, row])
    );

  it('puts two parallel reused steps both ending at the start of the run', () => {
    const rows = placed(
      runOf(
        [
          ['a', 3],
          ['b', 8],
        ],
        [skipped('a'), skipped('b')]
      )
    );
    expect(rows.get('a')?.shownEndMs).toBe(start);
    expect(rows.get('a')?.shownStartMs).toBe(start - 3_000);
    expect(rows.get('b')?.shownEndMs).toBe(start);
    expect(rows.get('b')?.shownStartMs).toBe(start - 8_000);
  });

  it('puts a diamond in dependency order: the join last, the head before both branches', () => {
    const rows = placed(
      runOf(
        [
          ['a', 4],
          ['b', 2],
          ['c', 6],
          ['d', 1],
        ],
        [
          skipped('a'),
          skipped('b', ['a']),
          skipped('c', ['a']),
          skipped('d', ['b', 'c']),
        ]
      )
    );
    expect(rows.get('d')?.shownEndMs).toBe(start);
    // Both branches end where the join starts.
    expect(rows.get('b')?.shownEndMs).toBe(start - 1_000);
    expect(rows.get('c')?.shownEndMs).toBe(start - 1_000);
    // The head ends where the earliest branch starts: the longer one, c.
    expect(rows.get('c')?.shownStartMs).toBe(start - 7_000);
    expect(rows.get('a')?.shownEndMs).toBe(start - 7_000);
    expect(rows.get('a')?.shownStartMs).toBe(start - 11_000);
  });

  it('never overlaps a dependency and leaves the steps that ran at their real time', () => {
    const run = runOf(
      [
        ['a', 4],
        ['b', 2],
        ['c', 6],
        ['d', 1],
      ],
      [
        skipped('a'),
        skipped('b', ['a']),
        skipped('c', ['a']),
        skipped('d', ['b', 'c']),
      ]
    );
    const rows = placed(run);
    for (const name of ['b', 'c', 'd']) {
      const row = rows.get(name)!;
      for (const dep of row.dependsOn ?? []) {
        expect(rows.get(dep)!.shownEndMs).toBeLessThanOrEqual(row.shownStartMs);
      }
    }
    // No ghost reaches into the run, and the step that ran stays put.
    for (const name of ['a', 'b', 'c', 'd']) {
      expect(rows.get(name)!.shownEndMs).toBeLessThanOrEqual(start);
    }
    expect(rows.get('use')?.shownStartMs).toBe(start);
    expect(rows.get('use')?.shownEndMs).toBe(start + 5_000);
  });
});
