// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ApiDAGDetails, DagDocModel } from '../contracts';
import {
  parseAllDocumentsAsServer,
  parseDagDocLenient,
  resolveStepNames,
} from '../doc';

/** Line ends and trailing blanks do not make a different definition. */
export function normalizeSpec(spec: string): string {
  return spec.replace(/\r\n/g, '\n').trimEnd();
}

/**
 * The difference between the definition that a run used (the snapshot) and
 * the definition now (the live text).
 */
export interface RunSpecDiff {
  /**
   * A top-level key other than `steps` and the handlers differs, a later
   * document (a local sub-DAG) differs, or a text cannot be read. Then every
   * step can behave differently.
   */
  headChanged: boolean;
  /** Steps in both definitions whose parsed definition differs. */
  changed: Set<string>;
  /** Steps of the snapshot that the live text no longer has. */
  removedFromLive: Set<string>;
  /** Steps of the live text that the snapshot did not have. */
  addedInLive: Set<string>;
}

/** Server names of the steps, when the caller knows them. */
export interface RunSpecNames {
  snapshot?: ApiDAGDetails;
  live?: ApiDAGDetails;
}

// Top-level keys that are not the head of the DAG: the steps, and the keys
// that describe, schedule, notify, retain, or limit runs, which cannot change
// what a step gives. The server leaves out the same keys (intake/reuse.go).
const NON_HEAD_KEYS: ReadonlySet<string> = new Set([
  'steps',
  'group',
  'description',
  'labels',
  'tags',
  'schedule',
  'skip_if_successful',
  'catchup_window',
  'overlap_policy',
  'webhook',
  'watch',
  'smtp',
  'mail_accounts',
  'mail_on',
  'error_mail',
  'info_mail',
  'wait_mail',
  'hist_retention_days',
  'hist_retention_runs',
  'slot_retention_days',
  'log_dir',
  'otel',
  'queue',
  'max_active_runs',
  'max_active_steps',
  'timeout_sec',
  'delay_sec',
  'restart_wait_sec',
  'restart_policy',
  'retry_policy',
  'max_clean_up_time_sec',
]);

// Keys of the handlers. Of them only init is the head: it runs before the
// steps, and the others after them.
const HANDLER_KEYS: ReadonlySet<string> = new Set(['handler_on', 'handlerOn']);

/** JSON text with sorted keys: equal values give equal text. */
export function canonical(value: unknown): string {
  const sorted = (v: unknown): unknown => {
    if (Array.isArray(v)) return v.map(sorted);
    if (v !== null && typeof v === 'object') {
      return Object.fromEntries(
        Object.entries(v as Record<string, unknown>)
          .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
          .map(([k, entry]) => [k, sorted(entry)])
      );
    }
    return v;
  };
  return JSON.stringify(sorted(value)) ?? 'undefined';
}

function headValue(js: unknown): unknown {
  if (js === null || js === undefined) return {};
  if (typeof js !== 'object' || Array.isArray(js)) return js;
  return Object.fromEntries(
    Object.entries(js as Record<string, unknown>).flatMap(([key, value]) => {
      if (NON_HEAD_KEYS.has(key)) return [];
      if (!HANDLER_KEYS.has(key)) return [[key, value]];
      const init =
        value !== null && typeof value === 'object' && !Array.isArray(value)
          ? (value as Record<string, unknown>)['init']
          : undefined;
      return init === undefined ? [] : [[key, { init }]];
    })
  );
}

/**
 * The parsed top-level keys without the steps, the keys that only describe
 * or schedule runs, and the handlers other than init, with the later
 * documents: they define the local sub-DAGs that steps can call. The server
 * compares the same head. Null when the text cannot be read.
 */
export function headOf(model: DagDocModel): string | null {
  try {
    const [doc, ...later] = parseAllDocumentsAsServer(
      model.sanitizedText ?? model.text
    );
    if (!doc || doc.errors.length > 0) return null;
    const head = headValue(doc.toJS());
    if (later.length === 0) return canonical(head);
    if (later.some((d) => d.errors.length > 0)) return null;
    return canonical([head, ...later.map((d) => d.toJS())]);
  } catch {
    return null;
  }
}

/**
 * Parsed definition of each step by name. A step of a chain DAG depends on
 * the steps of the position before it (`injectChainDependencies` in
 * internal/spec/builder.go), so those steps are part of its definition. A
 * parallel group is one position.
 */
function stepsByName(
  model: DagDocModel,
  details: ApiDAGDetails | undefined
): Map<string, string> {
  const names = resolveStepNames(model, details).byKey;
  const chain = model.dagType === 'chain';
  const out = new Map<string, string>();
  let previous: string[] = [];
  let current: string[] = [];
  let group: number | undefined;
  for (const step of model.steps) {
    // An unnamed step that the server did not name: the id, then the place.
    const name = names[step.key] ?? step.explicitName ?? step.id;
    const label = name ?? `#${step.index ?? out.size}`;
    if (step.parallelGroup === undefined || step.parallelGroup !== group) {
      previous = current;
      current = [];
      group = step.parallelGroup;
    }
    current.push(label);
    out.set(
      label,
      canonical(
        chain ? { step: step.value, after: [...previous].sort() } : step.value
      )
    );
  }
  return out;
}

/**
 * Compares two definitions as parsed values: comments and spacing do not
 * count. A step is matched by its name. A text that cannot be read gives
 * `headChanged`, the safe answer.
 */
export function diffRunSpec(
  snapshotText: string,
  liveText: string,
  names: RunSpecNames = {}
): RunSpecDiff {
  const result: RunSpecDiff = {
    headChanged: false,
    changed: new Set(),
    removedFromLive: new Set(),
    addedInLive: new Set(),
  };
  const snapshot = parseDagDocLenient(snapshotText);
  const live = parseDagDocLenient(liveText);
  if (!snapshot.ok || !live.ok) {
    result.headChanged = true;
    return result;
  }
  const snapshotHead = headOf(snapshot);
  const liveHead = headOf(live);
  result.headChanged =
    snapshotHead === null || liveHead === null || snapshotHead !== liveHead;

  const before = stepsByName(snapshot, names.snapshot);
  const after = stepsByName(live, names.live);
  for (const [name, text] of before) {
    const now = after.get(name);
    if (now === undefined) result.removedFromLive.add(name);
    else if (now !== text) result.changed.add(name);
  }
  for (const name of after.keys()) {
    if (!before.has(name)) result.addedInLive.add(name);
  }
  return result;
}

/** True when the two definitions differ as text after `normalizeSpec`. */
export function specTextDiffers(a: string, b: string): boolean {
  return normalizeSpec(a) !== normalizeSpec(b);
}
