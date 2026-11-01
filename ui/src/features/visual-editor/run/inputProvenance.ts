// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus, ReusedInputVia, type components } from '@/api/v1/schema';
import { isReusedNode } from './stepRunInfo';

type Node = components['schemas']['Node'];
type ReusedInput = components['schemas']['ReusedInput'];

/**
 * Where the outputs of a step that a run did not execute come from: 'run'
 * (carried from the run named by `outputsFromRunId`) or 'cache' (values that
 * the browser had read from a run, sent with the name of that run).
 */
export type InputSource = 'cache' | 'run';

/** The record of one reused step: the source of its outputs and its age. */
export interface InputProvenance {
  /** Server name of the step. */
  step: string;
  source: InputSource;
  /**
   * The run that gave the outputs. Always set for a record that the server
   * or the browser made; absent only for a run that an older version of the
   * labels recorded as cached.
   */
  runId?: string;
  /** The step had changed since the source was made, and the user ran anyway. */
  stale: boolean;
  /** Capture time of the values, in ms since the epoch; null when unknown. */
  capturedAt: number | null;
  /**
   * The times that the step had in the source run, in ms since the epoch: the
   * time that it took is `finishedAt - startedAt`. Absent for a record that
   * has none (a value that a client other than the web UI supplied, or an
   * older run).
   */
  startedAt?: number | null;
  finishedAt?: number | null;
}

/** The time that a reused step took in its source run, in ms; null when unknown. */
export function reusedDurationMs(p: InputProvenance): number | null {
  const { startedAt, finishedAt } = p;
  if (startedAt == null || finishedAt == null || finishedAt < startedAt) {
    return null;
  }
  return finishedAt - startedAt;
}

// Label that names the run whose outputs fed a step run: `dagu.outputs-from=<runId>`.
export const OUTPUTS_FROM_PREFIX = 'dagu.outputs-from=';

/** The id of the run that fed the outputs of a step run, read from its labels. */
export function outputsFromRunId(
  labels: readonly string[] | undefined
): string | null {
  const label = labels?.find((l) => l.startsWith(OUTPUTS_FROM_PREFIX));
  return label ? label.slice(OUTPUTS_FROM_PREFIX.length) || null : null;
}

/** Key of the labels that record the reused steps of a run. */
export const INPUT_LABEL_KEY = 'dagu.input';
const INPUT_LABEL_PREFIX = `${INPUT_LABEL_KEY}=`;

// The server keeps a label value of at most 255 characters, made of
// a-z, 0-9, '-', '_', '.' and '/', and it writes the value in lower case.
const MAX_VALUE_LENGTH = 255;
const SAFE = /^[a-z0-9_-]$/;

/**
 * Writes a name with the characters that a label value keeps unchanged: a
 * character outside a-z, 0-9, '-' and '_' becomes '.' and the two hex digits
 * of each of its UTF-8 bytes, so that a capital letter survives the lower
 * case of the server.
 */
export function encodeLabelName(name: string): string {
  let out = '';
  for (const byte of new TextEncoder().encode(name)) {
    const ch = String.fromCharCode(byte);
    out += SAFE.test(ch) ? ch : `.${byte.toString(16).padStart(2, '0')}`;
  }
  return out;
}

/** The name that `encodeLabelName` wrote; null when the text is not such a name. */
export function decodeLabelName(text: string): string | null {
  const bytes: number[] = [];
  for (let i = 0; i < text.length; i++) {
    const ch = text[i] as string;
    if (ch === '.') {
      const hex = text.slice(i + 1, i + 3);
      if (!/^[0-9a-f]{2}$/.test(hex)) return null;
      bytes.push(parseInt(hex, 16));
      i += 2;
    } else if (SAFE.test(ch)) {
      bytes.push(ch.charCodeAt(0));
    } else {
      return null;
    }
  }
  if (bytes.length === 0) return null;
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(
      Uint8Array.from(bytes)
    );
  } catch {
    return null;
  }
}

// A label of an earlier version can say `pinned`: the values were typed by
// the user, which is not possible any more. It reads as a cache record.
const KIND_OF: Record<string, Pick<InputProvenance, 'source' | 'stale'>> = {
  cache: { source: 'cache', stale: false },
  'cache-stale': { source: 'cache', stale: true },
  pinned: { source: 'cache', stale: false },
  run: { source: 'run', stale: false },
  'run-stale': { source: 'run', stale: true },
};

function kindOf(p: InputProvenance): string {
  return p.stale ? `${p.source}-stale` : p.source;
}

/**
 * The label of one reused step: `dagu.input=<kind>/<epoch s>/<step>` with
 * `/<run id>` after it for a run source. The kind is cache, cache-stale,
 * run or run-stale. The run record of the server holds the source of each
 * reused step; the label is written only for what that record lacks: a
 * source run that the step had changed since. The step and the run id are written by
 * `encodeLabelName`, because the server accepts only a narrow set of
 * characters. Null when the value is too long for a label.
 */
export function inputLabel(p: InputProvenance): string | null {
  const seconds =
    p.capturedAt === null ? 0 : Math.max(0, Math.floor(p.capturedAt / 1000));
  const parts = [kindOf(p), String(seconds), encodeLabelName(p.step)];
  if (p.source === 'run') {
    if (!p.runId) return null;
    parts.push(encodeLabelName(p.runId));
  }
  const value = parts.join('/');
  return value.length <= MAX_VALUE_LENGTH
    ? `${INPUT_LABEL_PREFIX}${value}`
    : null;
}

/** Reads one `dagu.input` label; null for any other label or a format that is not known. */
export function parseInputLabel(label: string): InputProvenance | null {
  if (!label.startsWith(INPUT_LABEL_PREFIX)) return null;
  const [kind, seconds, name, run, ...rest] = label
    .slice(INPUT_LABEL_PREFIX.length)
    .split('/');
  // A label can say `constructor`: only an own key of the table is a kind.
  const known =
    kind !== undefined && Object.prototype.hasOwnProperty.call(KIND_OF, kind)
      ? KIND_OF[kind]
      : undefined;
  if (!known || rest.length > 0 || !/^\d+$/.test(seconds ?? '')) return null;
  const step = decodeLabelName(name ?? '');
  if (step === null) return null;
  const epoch = Number(seconds);
  const capturedAt = epoch > 0 ? epoch * 1000 : null;
  if (known.source === 'run') {
    const runId = decodeLabelName(run ?? '');
    return runId === null ? null : { step, ...known, runId, capturedAt };
  }
  return run === undefined ? { step, ...known, capturedAt } : null;
}

/** The reused steps that the `dagu.input` labels name, by step name. */
export function parseInputLabels(
  labels: readonly string[] | undefined
): Map<string, InputProvenance> {
  const found = new Map<string, InputProvenance>();
  for (const label of labels ?? []) {
    const record = parseInputLabel(label);
    if (record) found.set(record.step, record);
  }
  return found;
}

/** What the provenance of a run reads of it. */
export interface ProvenanceRun {
  labels?: readonly string[];
  nodes?: readonly Node[];
  /** The record of the server: where the carried outputs come from. */
  reusedInputs?: readonly ReusedInput[];
  /** True for a test run. */
  ephemeral?: boolean;
}

/**
 * True when the run is a test run that the server drops after a short time.
 * The flag of the server decides; the label that an earlier version wrote is
 * read for a run that has no flag.
 */
export function isEphemeralRun(
  run: Pick<ProvenanceRun, 'labels' | 'ephemeral'> | null | undefined
): boolean {
  return (
    run?.ephemeral === true ||
    (run?.labels?.includes(EPHEMERAL_LABEL_OLD) ?? false)
  );
}

// The label that a version before the flag of the server wrote.
const EPHEMERAL_LABEL_OLD = 'dagu.ephemeral=true';

function finishedAtMs(node: Node): number | null {
  return nodeTime(node.finishedAt);
}

function isoMs(value: string | undefined): number | null {
  if (!value) return null;
  const time = Date.parse(value);
  return Number.isFinite(time) ? time : null;
}

/**
 * The sum of the times that the reused steps of a run took in their source
 * runs, in ms; null when the run reused no step or no time is known. It is
 * shown next to the duration of the run and never added to it.
 */
export function reusedTimeMs(
  run: ProvenanceRun | null | undefined
): number | null {
  let sum: number | null = null;
  for (const record of runInputProvenance(run).values()) {
    const took = reusedDurationMs(record);
    if (took !== null) sum = (sum ?? 0) + took;
  }
  return sum;
}

function nodeTime(value: string | undefined): number | null {
  if (!value || value === '-' || value.startsWith('0001-01-01')) return null;
  const time = Date.parse(value);
  return Number.isFinite(time) ? time : null;
}

/**
 * The steps that a run reused, read from the record of the server
 * (`reusedInputs`), by step name; null when the run has no record. The only
 * thing that the record lacks is the `stale` flag, which the
 * `dagu.input` labels carry.
 */
export function fromDetails(
  run: ProvenanceRun | null | undefined
): Map<string, InputProvenance> | null {
  const record = run?.reusedInputs;
  if (!run || !record || record.length === 0) return null;
  const stale = parseInputLabels(run.labels);
  const found = new Map<string, InputProvenance>();
  for (const item of record) {
    found.set(item.step, {
      step: item.step,
      source: item.via === ReusedInputVia.cache ? 'cache' : 'run',
      runId: item.runId,
      stale: stale.get(item.step)?.stale ?? false,
      capturedAt: isoMs(item.capturedAt),
      startedAt: isoMs(item.startedAt),
      finishedAt: isoMs(item.finishedAt),
    });
  }
  return found;
}

/**
 * The steps that a run reused, by step name. The record of the server gives
 * them (`fromDetails`). A run that was started before the record gives them
 * through its `dagu.input` labels; one that has only `dagu.outputs-from`
 * gives a source `run` for each step that carries copied outputs. Handlers
 * and steps that ran are never in the result.
 */
export function runInputProvenance(
  run: ProvenanceRun | null | undefined
): ReadonlyMap<string, InputProvenance> {
  if (!run) return NONE;
  const found = fromDetails(run) ?? parseInputLabels(run.labels);
  const from = outputsFromRunId(run.labels);
  if (from !== null) {
    for (const node of run.nodes ?? []) {
      const name = node.step.name;
      if (found.has(name) || !isReusedNode(node)) continue;
      found.set(name, {
        step: name,
        source: 'run',
        runId: from,
        stale: false,
        capturedAt: finishedAtMs(node),
        startedAt: nodeTime(node.startedAt),
        finishedAt: finishedAtMs(node),
      });
    }
  }
  // A skipped node carries the times that the step had in its source run: they
  // fill the times that a record or a label lacks.
  for (const node of run.nodes ?? []) {
    const record = found.get(node.step.name);
    if (!record || !isReusedNode(node)) continue;
    if (record.startedAt == null) record.startedAt = nodeTime(node.startedAt);
    if (record.finishedAt == null) record.finishedAt = finishedAtMs(node);
  }
  // A label can name a step that the run executed anyway (a run that the
  // user retried): only a step that did not run is a reused one.
  for (const node of run.nodes ?? []) {
    if (
      node.status !== NodeStatus.NotStarted &&
      node.status !== NodeStatus.Skipped
    ) {
      found.delete(node.step.name);
    }
  }
  return found.size === 0 ? NONE : found;
}

const NONE: ReadonlyMap<string, InputProvenance> = new Map();

/** A run that gives outputs of one step, with the times that the step had in it. */
export interface StepRunRef {
  runId: string;
  /** RFC 3339 times of the node of the step in that run. */
  startedAt?: string;
  finishedAt?: string;
}

/**
 * The record of the previous steps that a step run does not execute, as the
 * browser knows it when it starts the run. A step with a run of its own in
 * `fromRuns` (sent as `outputsFromRuns`) comes from that run; another step
 * that the source run holds comes from the source run, with the times of its
 * node there. A step that the run executes, and a step that nothing covers,
 * have no record. `stale` lists the steps that changed since the source run.
 */
export function reusedInputs(p: {
  previous: readonly string[];
  /** Server names of the steps that the request executes, with the step. */
  steps: readonly string[];
  /** The run of each step that has one of its own. */
  fromRuns: Readonly<Record<string, StepRunRef | undefined>>;
  /** The run named by `outputsFromRunId`, or null. */
  source: {
    dagRunId: string;
    startedAt?: string;
    finishedAt?: string;
    nodes?: readonly Node[];
  } | null;
  stale: readonly string[];
  missing: readonly string[];
}): InputProvenance[] {
  const { source } = p;
  const records: InputProvenance[] = [];
  for (const step of p.previous) {
    if (p.steps.includes(step)) continue;
    const own = p.fromRuns[step];
    if (own) {
      records.push({
        step,
        source: 'run',
        runId: own.runId,
        stale: false,
        capturedAt: isoMs(own.finishedAt),
        startedAt: isoMs(own.startedAt),
        finishedAt: isoMs(own.finishedAt),
      });
    } else if (source && !p.missing.includes(step)) {
      const node = source.nodes?.find((n) => n.step.name === step);
      const finished = node ? finishedAtMs(node) : null;
      records.push({
        step,
        source: 'run',
        runId: source.dagRunId,
        stale: p.stale.includes(step),
        capturedAt: finished ?? isoMs(source.finishedAt || source.startedAt),
        startedAt: node ? nodeTime(node.startedAt) : null,
        finishedAt: finished,
      });
    }
  }
  return records;
}
