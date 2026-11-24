// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useState } from 'react';
import { Status } from '@/api/v1/schema';
import type { RestartPolicy } from '../doc/ops/restart';

/**
 * The time that the alive state outlasts the wait before a new run: the
 * scheduler scans, queues and starts the next run within it.
 */
export const ALIVE_GRACE_MS = 10_000;

/** The end of a hold that counts from the end time of a run; 0 without one. */
function holdFrom(finishedAt: string | undefined, holdMs: number): number {
  const ended = finishedAt ? Date.parse(finishedAt) : NaN;
  return Number.isFinite(ended) ? ended + holdMs : 0;
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
 * The hold counts from the moment the hook sees the run end, so the clock of
 * the browser is not compared with the end time of the server. Only a run
 * that had ended before the hook mounted counts from its end time.
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
  const [seen, setSeen] = useState(() => ({
    key,
    until: running ? 0 : holdFrom(finishedAt, holdMs),
  }));
  let until = seen.until;
  // Set during the render, so that no still frame shows between the run and
  // the hold.
  if (seen.key !== key) {
    if (!running && holdFrom(finishedAt, holdMs) > 0) {
      until = Date.now() + holdMs;
    }
    setSeen({ key, until });
  }
  const idle = running || policy === 'no' || suspended || stopped;
  // Draws again when the hold ends.
  useEffect(() => {
    const left = until - Date.now();
    if (idle || left <= 0) return;
    const timer = window.setTimeout(
      () => setSeen((state) => ({ ...state, until: 0 })),
      left
    );
    return () => window.clearTimeout(timer);
  }, [until, idle]);
  return (
    policy !== 'no' && !suspended && !stopped && (running || Date.now() < until)
  );
}
