// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Status } from '@/api/v1/schema';
import { isActive, missingIn, type ReuseRun } from './reuseSource';

/** A run of the history, as the choice of a source reads it. */
export type SourceRun = ReuseRun & { ephemeral?: boolean };

/** Why an option cannot be chosen. */
export type OptionBlock =
  | { reason: 'missing'; steps: string[] }
  | { reason: 'active' };

/** One line of the run picker. */
export interface SourceOption {
  /** 'run:<id>' for a run, 'cache' for the cached values. */
  id: string;
  kind: 'run' | 'cache';
  /** The run, for kind 'run'. */
  runId?: string;
  status?: Status;
  startedAt?: string;
  finishedAt?: string;
  /** The run is a test run that still exists. */
  ephemeral?: boolean;
  /** The option is the automatic choice (`pickReuseSource`). */
  auto?: boolean;
  /** The option cannot be chosen, and why. */
  blocked?: OptionBlock;
  /** The runs that the cached values were read from, and the oldest capture time. */
  cache?: { runIds: string[]; capturedAt: number };
}

export const CACHE_OPTION_ID = 'cache';

export function runOptionId(runId: string): string {
  return `run:${runId}`;
}

function optionOf(
  run: SourceRun,
  reads: readonly string[],
  auto: boolean
): SourceOption {
  const missing = missingIn(run, reads);
  const blocked: OptionBlock | undefined = isActive(run)
    ? { reason: 'active' }
    : missing.length > 0
      ? { reason: 'missing', steps: missing }
      : undefined;
  return {
    id: runOptionId(run.dagRunId),
    kind: 'run',
    runId: run.dagRunId,
    status: run.status,
    ...(run.startedAt ? { startedAt: run.startedAt } : {}),
    ...(run.finishedAt ? { finishedAt: run.finishedAt } : {}),
    ...(run.ephemeral ? { ephemeral: true } : {}),
    ...(auto ? { auto: true } : {}),
    ...(blocked ? { blocked } : {}),
  };
}

/**
 * The lines of the run picker. The automatic choice is first, then the other
 * runs of the history, newest first as `runs` gives them; a run that does not
 * hold every output that the step reads, or that still runs, is blocked with
 * its reason. The cached values come last, and only with the runs that they
 * were read from.
 */
export function buildSourceOptions(p: {
  /** The runs of the history, newest first, test runs included. */
  runs: readonly SourceRun[];
  /** The result of `pickReuseSource`. */
  auto: SourceRun | null;
  /** The steps whose outputs the step reads. */
  reads: readonly string[];
  /** The fresh cache entries; null when the cache covers no previous step. */
  cache: { runIds: readonly string[]; capturedAt: number } | null;
}): SourceOption[] {
  const options: SourceOption[] = [];
  if (p.auto) options.push(optionOf(p.auto, p.reads, true));
  for (const run of p.runs) {
    if (p.auto && run.dagRunId === p.auto.dagRunId) continue;
    options.push(optionOf(run, p.reads, false));
  }
  if (p.cache && p.cache.runIds.length > 0) {
    options.push({
      id: CACHE_OPTION_ID,
      kind: 'cache',
      cache: { runIds: [...p.cache.runIds], capturedAt: p.cache.capturedAt },
    });
  }
  return options;
}
