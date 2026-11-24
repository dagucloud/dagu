// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useReducer, useState } from 'react';
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
 * True while the workflow of an Always on card is alive: its latest run is
 * running, queued or waiting, or it ended less than the wait before a new run
 * (and a grace) ago, so that the next run follows with no gap. The backoff
 * after runs that fail soon can outlast the hold: the card is then still
 * until the next run. A suspended workflow is not alive. With
 * `unless-stopped`, a run that an operator aborted is the end: nothing
 * follows it.
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
}): boolean {
  const running =
    status === Status.Running ||
    status === Status.Queued ||
    status === Status.Waiting;
  const stopped = policy === 'unless-stopped' && status === Status.Aborted;
  const holdMs = (waitSec ?? 0) * 1000 + ALIVE_GRACE_MS;
  const key = running ? 'running' : (finishedAt ?? '');
  // `from` is the moment the hold counts from, in ms; 0 without a hold.
  const [seen, setSeen] = useState(() => ({
    key,
    from: running ? 0 : endedAt(finishedAt),
  }));
  let from = seen.from;
  // Set during the render, so that no still frame shows between the run and
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
  const until = from ? from + holdMs : 0;
  const idle = running || policy === 'no' || suspended || stopped;
  const [, redraw] = useReducer((count: number) => count + 1, 0);
  // Draws again when the hold ends.
  useEffect(() => {
    const left = until - Date.now();
    if (idle || left <= 0) return;
    const timer = window.setTimeout(redraw, left);
    return () => window.clearTimeout(timer);
  }, [until, idle]);
  return (
    policy !== 'no' && !suspended && !stopped && (running || Date.now() < until)
  );
}
