// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  reusedDurationMs,
  runInputProvenance,
  type InputProvenance,
} from '@/features/visual-editor/run/inputProvenance';
import dayjs from '@/lib/dayjs';
import { isWholeSecondSpan } from '@/lib/formatStepSpan';
import { components, NodeStatus, Status } from '../../../../api/v1/schema';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type Node = components['schemas']['Node'];
type SubDAGRun = components['schemas']['SubDAGRun'];
type SubDAGRunDetail = components['schemas']['SubDAGRunDetail'];

/** The lifecycle handler nodes of a run, in the order that the run records them. */
const HANDLER_FIELDS = [
  'onInit',
  'onWait',
  'onSuccess',
  'onFailure',
  'onAbort',
  'onExit',
] as const;

/** Render-ready timeline row for either a parent step or expanded sub-DAG run. */
export type TimelineRow = {
  id: string;
  kind: 'step' | 'subdag';
  label: string;
  parentStepName?: string;
  startMs: number;
  endMs: number;
  status: NodeStatus | Status;
  statusLabel?: string;
  statusSource: 'node' | 'dagrun';
  description?: string;
  error?: string;
  params?: string;
  dagRunId?: string;
  dagName?: string;
  depth: number;
  /**
   * Names of the steps that this step waits for: its `depends`, the
   * dependencies that the UI infers from output references, and the routers
   * that route to it. Set on step rows only.
   */
  dependsOn?: string[];
  /**
   * True when the times of the row have a resolution of one second (no
   * fraction in the API values): a run that an older version stored. A span
   * of 0 reads "<1s" then, and "0ms" for a new run.
   */
  wholeSecond?: boolean;
  /**
   * Set on the row of a step that the run reused from another run. The row is
   * a ghost: it stands for time that the step took in that run, not in this
   * one. Its bar is as long as the original duration and stands before the
   * start of the run, as if the step had run just before it, whatever the
   * wall-clock time of the source run.
   */
  ghost?: GhostRow;
};

/** What a ghost row knows of the run that the step was reused from. */
export type GhostRow = {
  /** The run that the outputs of the step come from. */
  runId: string;
  /** How the outputs reached the run: carried from a run, or from the cache. */
  source: InputProvenance['source'];
  /** The time that the step took in that run, in ms. */
  durationMs: number;
  /** The times of the step in that run, in ms since the epoch; null when unknown. */
  startedAt: number | null;
  finishedAt: number | null;
  /** The start of this run, in ms: the ghosts stand before it. */
  runStartMs: number;
};

/** API query context needed to load sub-DAG runs for a root or nested DAG run. */
export type SubRunQueryContext = {
  rootDagName: string;
  rootDagRunId: string;
  parentSubDAGRunId?: string;
};

/** Returns child runs that should expand under a parent timeline row. */
export function getTimelineSubRuns(node: Node): SubDAGRun[] {
  if (node.step.parallel) {
    return node.subRuns || [];
  }
  if (!node.step.repeatPolicy?.repeat) {
    return [];
  }
  return [...(node.subRunsRepeated || []), ...(node.subRuns || [])];
}

/** Checks whether a DAG run has any expandable child sub-runs. */
export function hasTimelineSubRuns(dagRun: DAGRunDetails): boolean {
  return (dagRun.nodes || []).some(
    (node) => getTimelineSubRuns(node).length > 0
  );
}

/** Builds the root-aware query context for fetching sub-DAG run details. */
export function getSubRunQueryContext(
  dagRun: DAGRunDetails
): SubRunQueryContext {
  const rootDagName = dagRun.rootDAGRunName || dagRun.name;
  const rootDagRunId = dagRun.rootDAGRunId || dagRun.dagRunId;
  const parentSubDAGRunId =
    dagRun.dagRunId !== rootDagRunId ? dagRun.dagRunId : undefined;

  return {
    rootDagName,
    rootDagRunId,
    parentSubDAGRunId,
  };
}

/** Builds sorted timeline rows from parent nodes and loaded sub-DAG details. */
export function buildTimelineRows({
  dagRun,
  subRunDetails,
  nowMs,
}: {
  dagRun: DAGRunDetails;
  subRunDetails: SubDAGRunDetail[];
  nowMs: number;
}): TimelineRow[] {
  // The handlers run after the steps: the sort by start puts them at the end.
  const routers = routersByTarget(dagRun.nodes || []);
  const handlerRows = HANDLER_FIELDS.flatMap((event) => {
    const node = dagRun[event];
    return node ? [buildStepRow(node, nowMs, undefined, event)] : [];
  });
  // A step that the run reused is a ghost row, not a row at the wall-clock
  // time of its source run: its bar starts where the run starts.
  const reused = runInputProvenance(dagRun);
  const executed = (dagRun.nodes || []).filter(
    (node) => !reusedNode(node, reused)
  );
  const executedRows = [
    ...executed.map((node) =>
      buildStepRow(node, nowMs, routers.get(node.step.name))
    ),
    ...handlerRows,
  ].filter((row): row is TimelineRow & { node: Node } => row !== null);
  const runStartMs = runAxisStart(dagRun, executedRows, nowMs);
  const ghostRows = buildGhostRows(
    (dagRun.nodes || []).flatMap((node) => {
      const record = reusedNode(node, reused);
      return record ? [{ node, record }] : [];
    }),
    runStartMs,
    routers
  );
  const parentRows = [...ghostRows, ...executedRows].sort(
    (a, b) => a.startMs - b.startMs
  );

  const subRunDetailsById = new Map(
    subRunDetails.map((subRunDetail) => [subRunDetail.dagRunId, subRunDetail])
  );
  const rows: TimelineRow[] = [];

  for (const parentRow of parentRows) {
    const { node, ...row } = parentRow;
    rows.push(row);

    const subRuns = getTimelineSubRuns(node);
    subRuns.forEach((subRun, index) => {
      const detail = subRunDetailsById.get(subRun.dagRunId);
      if (!detail) {
        return;
      }

      const childRow = buildSubDAGRow({
        parentStepName: node.step.name,
        subRun,
        detail,
        index,
        nowMs,
      });
      if (childRow) {
        rows.push(childRow);
      }
    });
  }

  return rows;
}

/** The provenance of a node that the run reused, or null for a node that ran. */
function reusedNode(
  node: Node,
  reused: ReadonlyMap<string, InputProvenance>
): InputProvenance | null {
  const record = reused.get(node.step.name);
  return record && node.status === NodeStatus.Skipped ? record : null;
}

/** The start of the run axis: the earliest executed step, else the run, else now. */
function runAxisStart(
  dagRun: DAGRunDetails,
  rows: readonly { startMs: number }[],
  nowMs: number
): number {
  if (rows.length > 0) return Math.min(...rows.map((row) => row.startMs));
  const started = dagRun.startedAt ? dayjs(dagRun.startedAt).valueOf() : NaN;
  return Number.isNaN(started) ? nowMs : started;
}

/** The steps that a step waits for, by step name: depends, inferred, routers. */
function waitsFor(node: Node, routedFrom?: readonly string[]): string[] {
  return [
    ...new Set([
      ...(node.step.depends ?? []),
      ...(node.step.inferredDepends ?? []),
      ...(routedFrom ?? []),
    ]),
  ];
}

/**
 * The ghost rows of the reused steps. They stand before the start of the run,
 * as if the steps had run just before it: a reused step that no other reused
 * step waits for ends where the run starts, and every other one ends where the
 * earliest reused step that waits for it starts. Each keeps the time that it
 * took in its source run. A step whose time is unknown has no row.
 */
function buildGhostRows(
  reused: readonly { node: Node; record: InputProvenance }[],
  runStartMs: number,
  routers: ReadonlyMap<string, string[]>
): (TimelineRow & { node: Node })[] {
  const known = reused.flatMap(({ node, record }) => {
    const durationMs = reusedDurationMs(record);
    return durationMs === null || !record.runId
      ? []
      : [{ node, record, durationMs, runId: record.runId }];
  });
  const waits = new Map(
    known.map(({ node }) => [
      node.step.name,
      waitsFor(node, routers.get(node.step.name)),
    ])
  );
  const durations = new Map(
    known.map(({ node, durationMs }) => [node.step.name, durationMs])
  );
  const ends = new Map<string, number>();
  const endOf = (name: string, path: ReadonlySet<string>): number => {
    const cached = ends.get(name);
    if (cached !== undefined) return cached;
    // The earliest start of the reused steps that wait for this one.
    let end = runStartMs;
    for (const [other, needs] of waits) {
      if (other === name || path.has(other) || !needs.includes(name)) continue;
      const otherEnd = endOf(other, new Set([...path, name]));
      end = Math.min(end, otherEnd - (durations.get(other) ?? 0));
    }
    ends.set(name, end);
    return end;
  };
  return known.map(({ node, record, durationMs, runId }) => {
    const endMs = endOf(node.step.name, new Set());
    return {
      id: `step:${node.step.name}`,
      kind: 'step',
      label: node.step.name,
      startMs: endMs - durationMs,
      endMs,
      status: node.status,
      statusLabel: node.statusLabel,
      statusSource: 'node',
      description: node.step.description,
      depth: 0,
      dependsOn: waits.get(node.step.name),
      ghost: {
        runId,
        source: record.source,
        durationMs,
        startedAt: record.startedAt ?? null,
        finishedAt: record.finishedAt ?? null,
        runStartMs,
      },
      node,
    };
  });
}

/** The steps that route to each step, by the step name of the target. */
function routersByTarget(nodes: readonly Node[]): Map<string, string[]> {
  const byTarget = new Map<string, string[]>();
  for (const { step } of nodes) {
    for (const route of step.router?.routes ?? []) {
      for (const target of route.targets) {
        byTarget.set(target, [...(byTarget.get(target) ?? []), step.name]);
      }
    }
  }
  return byTarget;
}

function buildStepRow(
  node: Node,
  nowMs: number,
  routedFrom?: readonly string[],
  handlerEvent?: string
): (TimelineRow & { node: Node }) | null {
  if (!node.startedAt || node.startedAt === '-') {
    return null;
  }

  const startMs = dayjs(node.startedAt).valueOf();
  const endMs = parseEndMs(node.finishedAt, nowMs);
  const normalized = normalizeTiming(startMs, endMs);

  if (!normalized) {
    return null;
  }

  return {
    id: handlerEvent ? `handler:${handlerEvent}` : `step:${node.step.name}`,
    kind: 'step',
    label: node.step.name,
    startMs: normalized.startMs,
    endMs: normalized.endMs,
    status: node.status,
    statusLabel: node.statusLabel,
    statusSource: 'node',
    wholeSecond: isWholeSecondSpan(node.startedAt, node.finishedAt),
    description: node.step.description,
    error: node.error,
    depth: 0,
    dependsOn: handlerEvent ? undefined : waitsFor(node, routedFrom),
    node,
  };
}

function buildSubDAGRow({
  parentStepName,
  subRun,
  detail,
  index,
  nowMs,
}: {
  parentStepName: string;
  subRun: SubDAGRun;
  detail: SubDAGRunDetail;
  index: number;
  nowMs: number;
}): TimelineRow | null {
  const startMs = dayjs(detail.startedAt).valueOf();
  const endMs = parseEndMs(detail.finishedAt, nowMs);
  const normalized = normalizeTiming(startMs, endMs);

  if (!normalized) {
    return null;
  }

  return {
    id: `subdag:${parentStepName}:${detail.dagRunId}`,
    kind: 'subdag',
    label: `#${String(index + 1).padStart(2, '0')}`,
    parentStepName,
    startMs: normalized.startMs,
    endMs: normalized.endMs,
    status: detail.status,
    statusLabel: detail.statusLabel,
    statusSource: 'dagrun',
    wholeSecond: isWholeSecondSpan(detail.startedAt, detail.finishedAt),
    params: detail.params ?? subRun.params,
    dagRunId: detail.dagRunId,
    dagName: detail.dagName ?? subRun.dagName,
    depth: 1,
  };
}

function parseEndMs(timestamp: string | undefined, nowMs: number): number {
  if (!timestamp || timestamp === '-') {
    return nowMs;
  }
  return dayjs(timestamp).valueOf();
}

function normalizeTiming(
  startMs: number,
  endMs: number
): { startMs: number; endMs: number } | null {
  if (Number.isNaN(startMs) || Number.isNaN(endMs)) {
    return null;
  }

  if (endMs < startMs) {
    return { startMs, endMs: startMs + 100 };
  }

  return { startMs, endMs };
}

/** Axis tick: position in percent of the chart width and its time label. */
export type TimeMarker = { position: number; label: string };

/** Width of one character of an axis label: text-xs in the mono face. */
const AXIS_CHAR_PX = 7.2;
/** Room between the chart edge and the first or the last label. */
const AXIS_EDGE_PX = 8;
/** Room that two labels keep between them. */
const AXIS_GAP_PX = 8;

/**
 * The text of an axis tick. A span of a day or more names the date, so that
 * the axis never reads as hours of one day: "Jan 5 14:00" for ticks of hours,
 * "Jan 5" for ticks of days, "Jan 2025" for ticks of months and "2025" for
 * ticks of years. A shorter span shows the time of day, with the digits of the
 * second that the gap between ticks needs.
 */
function axisLabel(ms: number, stepMs: number, spanMs: number): string {
  const day = 86_400_000;
  // A gap longer than the longest month or year puts each tick in its own.
  if (stepMs > 366 * day) return dayjs(ms).format('YYYY');
  if (stepMs > 31 * day) return dayjs(ms).format('MMM YYYY');
  if (stepMs >= day) return dayjs(ms).format('MMM D');
  if (spanMs >= day) return dayjs(ms).format('MMM D HH:mm');
  // dayjs has no one-digit fraction token: cut the milliseconds to tenths.
  const decimals = stepMs < 100 ? 3 : stepMs < 1000 ? 1 : 0;
  const label = dayjs(ms).format(decimals ? 'HH:mm:ss.SSS' : 'HH:mm:ss');
  return decimals === 1 ? label.slice(0, -2) : label;
}

function evenMarkers(
  startMs: number,
  endMs: number,
  markerCount: number
): TimeMarker[] {
  const spanMs = endMs - startMs;
  const stepMs = spanMs / markerCount;
  const markers: TimeMarker[] = [];
  for (let i = 0; i <= markerCount; i++) {
    markers.push({
      position: (i / markerCount) * 100,
      label: axisLabel(startMs + stepMs * i, stepMs, spanMs),
    });
  }
  return markers;
}

/**
 * Whether the labels keep apart at this width. The first label starts at the
 * left edge, the last one ends at the right edge, the others are centred on
 * their tick: the layout of the axis header.
 */
function labelsFit(markers: readonly TimeMarker[], widthPx: number): boolean {
  let previousRight = -Infinity;
  return markers.every((marker, i) => {
    const width = marker.label.length * AXIS_CHAR_PX;
    const centre = (marker.position / 100) * widthPx;
    const left =
      i === 0
        ? AXIS_EDGE_PX
        : i === markers.length - 1
          ? widthPx - AXIS_EDGE_PX - width
          : centre - width / 2;
    const ok = left >= 0 && left - previousRight >= AXIS_GAP_PX;
    previousRight = left + width;
    return ok && previousRight <= widthPx;
  });
}

/**
 * Evenly spaced axis ticks. The label shows sub-second digits when the gap
 * between two ticks is under one second, so no two labels read the same.
 * With `widthPx`, the axis has the most ticks (up to `markerCount` gaps) whose
 * labels keep apart at that width: a narrow chart drops labels, it never
 * prints them over each other. When not even two labels fit, only the first
 * one stays.
 */
export function buildTimeMarkers(
  startMs: number,
  endMs: number,
  widthPx?: number,
  markerCount = 5
): TimeMarker[] {
  if (widthPx === undefined) return evenMarkers(startMs, endMs, markerCount);
  for (let count = markerCount; count >= 1; count--) {
    const markers = evenMarkers(startMs, endMs, count);
    if (labelsFit(markers, widthPx)) return markers;
  }
  return evenMarkers(startMs, endMs, 1).slice(0, 1);
}

/** Share of the axis span that the thinnest drawn bar takes. */
export const MIN_BAR_SPAN_FRACTION = 0.01;
/** Width in px of the thinnest drawn bar. */
export const MIN_BAR_PX = 4;
/** Chart width assumed for the thinnest bar before the chart is measured. */
const DEFAULT_CHART_PX = 600;

/**
 * The shortest span that a bar shows, in ms: the larger of one percent of
 * the axis span and 4 px of it. A step that took 0 ms (a run that an older
 * version stored has a resolution of one second) is drawn this wide.
 */
export function minBarMs(spanMs: number, widthPx?: number): number {
  const pxMs = (MIN_BAR_PX / (widthPx || DEFAULT_CHART_PX)) * spanMs;
  return Math.max(MIN_BAR_SPAN_FRACTION * spanMs, pxMs);
}

/** A row with the interval that the chart draws. */
export type PlacedRow = TimelineRow & {
  /** Drawn start: the real start, or the end of the last dependency. */
  shownStartMs: number;
  /** Drawn end: the real end, or the drawn start plus the thinnest bar. */
  shownEndMs: number;
  /** The dependency that pushed the drawn start, when it did. */
  shiftedAfter?: string;
};

/**
 * Gives each row the interval to draw and orders the rows. The times of a run
 * that an older version stored have a resolution of one second: a fast step
 * and its successor carry the same time. A new run has millisecond times and
 * keeps its real order. The steps go in topological order. The drawn start is the later of
 * the real start and the drawn end of each dependency. The drawn end is the
 * later of the real end and the drawn start plus `minVisibleMs`, so a step of
 * 0 ms is a thin bar and the chain reads as a staircase. Parallel steps keep
 * their real overlap. A dependency that has no row is ignored, and so is a
 * dependency cycle. The rows are ordered by drawn start, then by topological
 * order; a child row follows its parent and starts no earlier than it. A ghost
 * row (a step reused from another run) stands before the start of the run and
 * waits only for other ghosts: it never pushes a step that ran, so the real
 * times stay as they are. The ghosts come first, in dependency order, so that
 * a step that ran is below the ghosts that it depends on.
 */
export function placeTimelineRows(
  rows: readonly TimelineRow[],
  minVisibleMs: number
): PlacedRow[] {
  const groups: TimelineRow[][] = [];
  for (const row of rows) {
    if (row.depth === 0 || groups.length === 0) groups.push([row]);
    else groups[groups.length - 1]!.push(row);
  }
  const parents = groups.map((group) => group[0]!);
  const byName = new Map<string, number>();
  parents.forEach((row, i) => {
    if (row.kind === 'step' && row.id.startsWith('step:')) {
      byName.set(row.label, i);
    }
  });
  const needs = parents.map((row) =>
    [...new Set(row.dependsOn ?? [])].flatMap((name) => {
      const at = byName.get(name);
      return at === undefined ? [] : [at];
    })
  );

  // Kahn order, ties by real start then by input order. A cycle leaves some
  // rows that never get free: they follow in input order.
  const topo: number[] = [];
  const done = new Set<number>();
  const byStart = parents
    .map((_, i) => i)
    .sort((a, b) => parents[a]!.startMs - parents[b]!.startMs || a - b);
  while (topo.length < parents.length) {
    const next =
      byStart.find(
        (i) => !done.has(i) && needs[i]!.every((d) => done.has(d))
      ) ?? byStart.find((i) => !done.has(i))!;
    done.add(next);
    topo.push(next);
  }

  const placed = new Map<number, PlacedRow>();
  for (const i of topo) {
    const row = parents[i]!;
    let shownStartMs = row.startMs;
    let shiftedAfter: string | undefined;
    for (const d of needs[i]!) {
      const dep = placed.get(d);
      // A ghost stands for time of another run: only a ghost waits for a
      // ghost, and a step that ran never waits for one.
      if (dep && Boolean(dep.ghost) !== Boolean(row.ghost)) continue;
      if (dep && dep.shownEndMs > shownStartMs) {
        shownStartMs = dep.shownEndMs;
        shiftedAfter = dep.label;
      }
    }
    placed.set(i, {
      ...row,
      shownStartMs,
      shownEndMs: Math.max(row.endMs, shownStartMs + minVisibleMs),
      ...(shiftedAfter !== undefined ? { shiftedAfter } : {}),
    });
  }

  const rank = new Map(topo.map((i, at) => [i, at]));
  return [...parents.keys()]
    .sort(
      (a, b) =>
        // The ghosts come first: a step that ran never sits above a ghost
        // that it waits for.
        Number(Boolean(placed.get(b)!.ghost)) -
          Number(Boolean(placed.get(a)!.ghost)) ||
        placed.get(a)!.shownStartMs - placed.get(b)!.shownStartMs ||
        rank.get(a)! - rank.get(b)!
    )
    .flatMap((i) => {
      const parent = placed.get(i)!;
      const children = groups[i]!.slice(1).map((child): PlacedRow => {
        const shownStartMs = Math.max(child.startMs, parent.shownStartMs);
        return {
          ...child,
          shownStartMs,
          shownEndMs: Math.max(child.endMs, shownStartMs + minVisibleMs),
        };
      });
      return [parent, ...children];
    });
}

/**
 * The nodes of a run in display order: by drawn start, then by dependency
 * order. A node that has not started keeps its place in the list.
 */
export function orderNodesForDisplay(
  dagRun: DAGRunDetails,
  nowMs: number
): Node[] {
  const nodes = dagRun.nodes || [];
  const placed = placeTimelineRows(
    buildTimelineRows({ dagRun, subRunDetails: [], nowMs }),
    1
  );
  const byId = new Map(nodes.map((node) => [`step:${node.step.name}`, node]));
  const order = placed.flatMap((row) => {
    const node = byId.get(row.id);
    return node ? [node] : [];
  });
  const started = new Set(order);
  let next = 0;
  return nodes.map((node) => (started.has(node) ? order[next++]! : node));
}
