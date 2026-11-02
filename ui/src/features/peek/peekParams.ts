// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { buildDAGPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { DAG_NAME_PATTERN } from '@/lib/dag-validation';

/**
 * What a peek shows: a workflow with its latest run, or one run of it. The
 * fields mirror the full page that the peek opens.
 */
export interface PeekTarget {
  /**
   * The /dags/:segment of the full page: the file of a workflow, or the DAG
   * name of a run, as the rows of the lists link them.
   */
  dag: string;
  /** The run to show; without it the peek shows the latest run. */
  run?: string;
  /** The dock tab that the full page opens on, such as 'artifacts'. */
  dock?: string;
}

/** True when both targets name the same workflow and the same run. */
export function samePeekTarget(a: PeekTarget, b: PeekTarget): boolean {
  return a.dag === b.dag && (a.run ?? null) === (b.run ?? null);
}

/** The query keys of a peek. A list page keeps them out of its own state. */
export const PEEK_KEYS = ['peek', 'peekRun', 'peekDock'] as const;

const RUN_ID_PATTERN = /^[-a-zA-Z0-9_]+$/;
const DOCK_PATTERN = /^[a-z]+$/;

/** Reports whether the server can have the workflow and the run of a target. */
export function isPeekTarget(target: PeekTarget): boolean {
  return (
    DAG_NAME_PATTERN.test(target.dag) &&
    // The pattern admits the directory names, which name no workflow.
    target.dag !== '.' &&
    target.dag !== '..' &&
    (target.run === undefined || RUN_ID_PATTERN.test(target.run)) &&
    (target.dock === undefined || DOCK_PATTERN.test(target.dock))
  );
}

/** The peek of a query; null when it has none or names an invalid target. */
export function readPeek(params: URLSearchParams): PeekTarget | null {
  const dag = params.get('peek');
  if (!dag) return null;
  const target: PeekTarget = { dag };
  const run = params.get('peekRun');
  if (run) target.run = run;
  const dock = params.get('peekDock');
  if (dock) target.dock = dock;
  return isPeekTarget(target) ? target : null;
}

function toSearch(params: URLSearchParams): string {
  const query = params.toString();
  return query ? `?${query}` : '';
}

/** A location search without the peek keys. The other keys keep their order. */
export function withoutPeek(search: string): string {
  const params = new URLSearchParams(search);
  for (const key of PEEK_KEYS) params.delete(key);
  return toSearch(params);
}

/**
 * The full page of a target: the URL that the row of the target links. With
 * `step`, the page opens on that step.
 */
export function peekPageURL(
  target: PeekTarget,
  remoteNode: string,
  step?: string
): string {
  return buildDAGPageURL({
    fileName: target.dag,
    remoteNode,
    run: target.run,
    dock: target.dock,
    step,
  });
}
