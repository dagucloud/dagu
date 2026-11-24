// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useMemo, useReducer, useState } from 'react';
import { Status } from '@/api/v1/schema';
import type { RestartPolicy } from '../doc/ops/restart';

/**
 * The time that the alive state outlasts the wait before a new run: the
 * scheduler scans, queues and starts the next run within it.
 */
export const ALIVE_GRACE_MS = 10_000;

/** The end time of a run in ms; 0 without one. */
function endedAt(finishedAt: string | undefined): number {
  const ended = finishedAt ? Date.parse(finishedAt) : NaN;
  return Number.isFinite(ended) ? ended : 0;
}

/**
 * What an Always on card tells while its workflow is alive: a run is active,
 * the wait before the next run goes on, or the wait is over and the
 * scheduler starts the next run.
 */
export type AliveState =
  | { phase: 'running' }
  | {
      phase: 'waiting';
      /** The end of the wait, in ms of the browser clock. */
      nextRunAt: number;
    }
  | { phase: 'starting' };

/**
 * The state of the workflow of an Always on card, or null while it is not
 * alive. The latest run is running, queued or waiting, or it ended less than
 * the wait before a new run (and a grace) ago, so that the next run follows
 * with no gap. The backoff after runs that fail soon can outlast the hold:
 * the card is then idle until the next run. A suspended workflow is not
 * alive. With `unless-stopped`, an aborted run is taken as a stop by an
 * operator: nothing follows it. The run does not tell a stop from another
 * abort, so a run that unmet preconditions aborted is idle too, though the
 * next one follows.
 *
 * The hold counts from the moment the hook sees a run end, so the clock of
 * the browser is not compared with the end time of the server. A run that
 * the hook did not see running counts from its end time. The hold follows
 * the wait as it changes.
 */
export function useAlive({
  status,
  finishedAt,
  policy,
  waitSec,
  suspended,
}: {
  /** The status of the latest run of the workflow; undefined without a run. */
  status: Status | undefined;
  /** The end time of that run, as the server writes it; `-` or undefined while it runs. */
  finishedAt: string | undefined;
  policy: RestartPolicy;
  waitSec: number | null;
  suspended: boolean;
}): AliveState | null {
  const running =
    status === Status.Running ||
    status === Status.Queued ||
    status === Status.Waiting;
  const stopped = policy === 'unless-stopped' && status === Status.Aborted;
  const key = running ? 'running' : (finishedAt ?? '');
  // `from` is the moment the hold counts from, in ms; 0 without a hold.
  const [seen, setSeen] = useState(() => ({
    key,
    from: running ? 0 : endedAt(finishedAt),
  }));
  let from = seen.from;
  // Set during the render, so that no idle frame shows between the run and
  // the hold.
  if (seen.key !== key) {
    if (status === undefined) {
      from = 0;
    } else if (!running) {
      // A run with no end time yet keeps the hold of the run before it.
      from = seen.key === 'running' ? Date.now() : endedAt(finishedAt) || from;
    }
    setSeen({ key, from });
  }
  const due = from ? from + (waitSec ?? 0) * 1000 : 0;
  const until = from ? due + ALIVE_GRACE_MS : 0;
  const idle = running || policy === 'no' || suspended || stopped;
  const [tick, redraw] = useReducer((count: number) => count + 1, 0);
  // Draws again when the wait ends, and when the hold ends.
  useEffect(() => {
    const now = Date.now();
    if (idle || now >= until) return;
    const timer = window.setTimeout(redraw, (now < due ? due : until) - now);
    return () => window.clearTimeout(timer);
  }, [due, until, idle, tick]);
  const now = Date.now();
  const phase =
    policy === 'no' || suspended || stopped
      ? null
      : running
        ? 'running'
        : now < due
          ? 'waiting'
          : now < until
            ? 'starting'
            : null;
  // One object per state, so that the canvas draws again only when it changes.
  return useMemo(() => {
    if (phase === 'waiting') return { phase, nextRunAt: due };
    return phase ? { phase } : null;
  }, [phase, due]);
}

/**
 * The whole seconds left until `at`, in ms of the browser clock, drawn again
 * once a second; 0 once it has passed or without one. Only the component that
 * shows the count draws again.
 */
export function useSecondsLeft(at: number | null): number {
  const [tick, redraw] = useReducer((count: number) => count + 1, 0);
  useEffect(() => {
    const left = at === null ? 0 : at - Date.now();
    if (left <= 0) return;
    const timer = window.setTimeout(redraw, left % 1000 || 1000);
    return () => window.clearTimeout(timer);
  }, [at, tick]);
  const left = at === null ? 0 : at - Date.now();
  return left > 0 ? Math.ceil(left / 1000) : 0;
}
