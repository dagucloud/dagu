// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useState } from 'react';
import { Status } from '@/api/v1/schema';
import type { RestartPolicy } from '../doc/ops/restart';

/** The time that the alive state outlasts the wait before a new run. */
export const ALIVE_GRACE_MS = 2000;

/**
 * True while the workflow of an Always on card is alive: the run that the
 * page shows is running or queued, or it ended less than the wait before a
 * new run (and a short grace) ago, so that the next run follows with no gap.
 * A suspended workflow is not alive. With `unless-stopped`, a run that an
 * operator aborted is the end: nothing follows it.
 */
export function useAlive({
  status,
  finishedAt,
  policy,
  waitSec,
  suspended,
}: {
  /** The status of the run that the page shows; undefined without a run. */
  status: Status | undefined;
  /** The end time of that run, as the server writes it; `-` or undefined while it runs. */
  finishedAt: string | undefined;
  policy: RestartPolicy;
  waitSec: number | null;
  suspended: boolean;
}): boolean {
  const running = status === Status.Running || status === Status.Queued;
  const stopped = policy === 'unless-stopped' && status === Status.Aborted;
  const ended = finishedAt ? Date.parse(finishedAt) : NaN;
  const holdUntil = Number.isFinite(ended)
    ? ended + (waitSec ?? 0) * 1000 + ALIVE_GRACE_MS
    : 0;
  const [, render] = useState(0);
  // Draws again when the hold ends.
  useEffect(() => {
    const left = holdUntil - Date.now();
    if (running || left <= 0) return;
    const timer = window.setTimeout(() => render((n) => n + 1), left);
    return () => window.clearTimeout(timer);
  }, [holdUntil, running]);
  const alive = running || Date.now() < holdUntil;
  return policy !== 'no' && !suspended && !stopped && alive;
}
