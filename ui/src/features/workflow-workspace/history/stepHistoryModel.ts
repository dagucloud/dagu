// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus, type Status } from '@/api/v1/schema';
import { executionOrder } from '@/features/dags/components/dag-editor/stepOrder';
import {
  reusedDurationMs,
  runInputProvenance,
  type InputProvenance,
} from '@/features/visual-editor/run/inputProvenance';
import { stepParticipation } from '@/features/visual-editor/run/stepRunInfo';
import { isUnsetTime } from '@/lib/dagRunTiming';
import type { ApiDAGDetails, ApiDAGRunDetails, ApiNode } from '../contracts';
import { HANDLER_EVENTS } from '../data/handlerMapping';
import { nodesByStep } from '../data/runFacts';
import { medianMs } from '../dock/runExtents';
import { isTestRun, runTime } from '../dock/runTimelineModel';
import { stepReuse, type StepReuse } from '../dock/stepReuse';

/**
 * How a step took part in one run: it ran, the run reused it from another
 * run, the run skipped it, it never started, or the run has no such step.
 */
export type StepRunState =
  | 'ran'
  | 'reused'
  | 'skipped'
  | 'notStarted'
  | 'absent';

/** One run as one step saw it. */
export interface StepRunEntry {
  runId: string;
  /** When the run started, else when it was queued; null without either. */
  at: string | null;
  runStatus: Status;
  testRun: boolean;
  state: StepRunState;
  /** The status of the node of the step; null when the run has none. */
  status: NodeStatus | null;
  /** The times of the node, as the server sent them. */
  startedAt: string | null;
  finishedAt: string | null;
  /**
   * The time that the step took, in ms: its own time, or for a reused step
   * the time that it took in its source run. null when unknown.
   */
  durationMs: number | null;
  /** The step is running or retrying. */
  running: boolean;
  /** The run that a reused step comes from, when the run records it. */
  reusedFrom: string | null;
  /** Whether the source run of a reused step gave its outputs. */
  reuseOutcome: StepReuse['outcome'] | null;
  /** The first line of the error of the node. */
  error: string | null;
}

/** A row of the steps grid. */
export interface StepGridRow {
  /** Server name of the step. */
  step: string;
  /** A lifecycle handler. */
  handler: boolean;
  /** The definition no longer has the step: only past runs hold it. */
  removed: boolean;
}

/** The steps of a workflow by run: `cells[row][column]`, columns as the runs. */
export interface StepGrid {
  rows: StepGridRow[];
  cells: StepRunEntry[][];
}

/** What the runs of one step add up to. */
export interface StepHistorySummary {
  /** Finished runs where the step ran: no test run and no reused step. */
  counted: number;
  /** The counted runs where the step succeeded. */
  succeeded: number;
  /** The median time of the counted runs where the step succeeded. */
  usualMs: number | null;
  /**
   * The time of the oldest run of the failures that end the counted runs;
   * null when the newest counted run did not fail.
   */
  failingSince: string | null;
}

const ACTIVE: ReadonlySet<NodeStatus> = new Set([
  NodeStatus.Running,
  NodeStatus.Retrying,
]);

const FINISHED: ReadonlySet<NodeStatus> = new Set([
  NodeStatus.Success,
  NodeStatus.PartialSuccess,
  NodeStatus.Failed,
  NodeStatus.Aborted,
  NodeStatus.Rejected,
]);

const FAILED: ReadonlySet<NodeStatus> = new Set([
  NodeStatus.Failed,
  NodeStatus.Rejected,
]);

/** What every view of the history reads of one run. */
interface RunIndex {
  nodes: ReadonlyMap<string, ApiNode>;
  provenance: ReadonlyMap<string, InputProvenance>;
  reuse: ReadonlyMap<string, StepReuse>;
}

// Built once for each run object, for the grid and the step view alike. A
// new copy of a run (a poll, an event) gets its own index.
const indexes = new WeakMap<ApiDAGRunDetails, RunIndex>();

function indexOf(run: ApiDAGRunDetails): RunIndex {
  let index = indexes.get(run);
  if (!index) {
    index = {
      nodes: nodesByStep(run),
      provenance: runInputProvenance(run),
      reuse: stepReuse(run),
    };
    indexes.set(run, index);
  }
  return index;
}

function timeMs(value: string | undefined): number | null {
  if (isUnsetTime(value)) return null;
  const ms = Date.parse(value!);
  return Number.isFinite(ms) ? ms : null;
}

function ownDurationMs(node: ApiNode, now: number): number | null {
  const start = timeMs(node.startedAt);
  if (start === null) return null;
  const end = timeMs(node.finishedAt) ?? (ACTIVE.has(node.status) ? now : null);
  return end === null || end < start ? null : end - start;
}

function firstLine(text: string | undefined): string | null {
  const line = text
    ?.split(/\r?\n/)
    .map((part) => part.trim())
    .find((part) => part !== '');
  return line ?? null;
}

function setTime(value: string | undefined): string | null {
  return isUnsetTime(value) ? null : value!;
}

/** The entry of one step in one run. A running step counts up to `now`. */
function entryOf(
  run: ApiDAGRunDetails,
  step: string,
  now: number
): StepRunEntry {
  const index = indexOf(run);
  const node = index.nodes.get(step);
  const reuse = index.reuse.get(step);
  const participation = stepParticipation(node, reuse !== undefined);
  const state: StepRunState =
    participation === 'notRun'
      ? node?.status === NodeStatus.Skipped
        ? 'skipped'
        : 'notStarted'
      : participation;
  const provenance = index.provenance.get(step);
  let durationMs: number | null = null;
  if (node && state === 'ran') durationMs = ownDurationMs(node, now);
  else if (provenance && state === 'reused') {
    durationMs = reusedDurationMs(provenance);
  }
  return {
    runId: run.dagRunId,
    at: runTime(run) ?? null,
    runStatus: run.status,
    testRun: isTestRun(run),
    state,
    status: node?.status ?? null,
    startedAt: setTime(node?.startedAt),
    finishedAt: setTime(node?.finishedAt),
    durationMs,
    running: state === 'ran' && !!node && ACTIVE.has(node.status),
    reusedFrom: state === 'reused' ? (reuse?.runId ?? null) : null,
    reuseOutcome: state === 'reused' ? (reuse?.outcome ?? null) : null,
    error: firstLine(node?.error),
  };
}

/** The entries of one step, one for each run, in the order of the runs. */
export function stepHistory(
  runs: readonly ApiDAGRunDetails[],
  step: string,
  now: number
): StepRunEntry[] {
  return runs.map((run) => entryOf(run, step, now));
}

function handlerNames(
  dag: ApiDAGDetails,
  runs: readonly ApiDAGRunDetails[]
): Set<string> {
  const names = new Set<string>();
  for (const handler of Object.values(dag.handlerOn ?? {})) {
    if (handler?.name) names.add(handler.name);
  }
  for (const run of runs) {
    for (const event of HANDLER_EVENTS) {
      const name = run[event]?.step?.name;
      if (name) names.add(name);
    }
  }
  return names;
}

/**
 * The steps of the workflow by run. The rows are the steps in execution
 * order, the handlers after them, then the steps that only past runs hold,
 * newest run first. A definition without its steps marks no row removed.
 */
export function stepGrid(
  dag: ApiDAGDetails,
  runs: readonly ApiDAGRunDetails[],
  now: number
): StepGrid {
  const order = executionOrder(dag);
  const known = dag.steps ? new Set(order) : null;
  const names = [...order];
  const listed = new Set(order);
  for (let i = runs.length - 1; i >= 0; i -= 1) {
    for (const name of indexOf(runs[i]!).nodes.keys()) {
      if (listed.has(name)) continue;
      listed.add(name);
      names.push(name);
    }
  }
  const handlers = handlerNames(dag, runs);
  const rows = names.map(
    (step): StepGridRow => ({
      step,
      handler: handlers.has(step),
      removed: known !== null && !known.has(step),
    })
  );
  return {
    rows,
    cells: rows.map((row) => stepHistory(runs, row.step, now)),
  };
}

/** True for a run where the step ran and failed or was rejected. */
export function isFailedEntry(entry: StepRunEntry): boolean {
  return (
    entry.state === 'ran' && entry.status !== null && FAILED.has(entry.status)
  );
}

// An aborted step was stopped, by a user or a failure before it: it tells
// nothing about how the step itself does, so the rate leaves it out.
function isCounted(entry: StepRunEntry): boolean {
  return (
    entry.state === 'ran' &&
    !entry.testRun &&
    entry.status !== null &&
    entry.status !== NodeStatus.Aborted &&
    FINISHED.has(entry.status)
  );
}

/** The summary of the entries of one step, oldest first. */
export function summarizeStepHistory(
  entries: readonly StepRunEntry[]
): StepHistorySummary {
  const counted = entries.filter(isCounted);
  const succeeded = counted.filter((e) => e.status === NodeStatus.Success);
  const times = succeeded
    .map((e) => e.durationMs)
    .filter((ms): ms is number => ms !== null);
  let failingSince: string | null = null;
  for (let i = counted.length - 1; i >= 0; i -= 1) {
    const entry = counted[i]!;
    if (!isFailedEntry(entry)) break;
    failingSince = entry.at;
  }
  return {
    counted: counted.length,
    succeeded: succeeded.length,
    usualMs: times.length > 0 ? medianMs(times) : null,
    failingSince,
  };
}

/**
 * The runs with the live details of the selected run in place of its copy
 * in the history, which can be behind. The same list when nothing differs.
 */
export function withLiveRun(
  runs: readonly ApiDAGRunDetails[],
  live: ApiDAGRunDetails | null
): readonly ApiDAGRunDetails[] {
  if (!live) return runs;
  const at = runs.findIndex((run) => run.dagRunId === live.dagRunId);
  if (at < 0 || runs[at] === live) return runs;
  const next = [...runs];
  next[at] = live;
  return next;
}
