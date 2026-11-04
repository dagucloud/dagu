// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Status } from '@/api/v1/schema';
import { useClock } from '@/hooks/useClock';
import { isUnsetTime } from '@/lib/dagRunTiming';
import dayjs from '@/lib/dayjs';

interface RunTimes {
  startedAt: string;
  finishedAt: string | null;
  status: number;
}

/**
 * The duration of a run in the run lists, "-" before its start. It counts up
 * each second while the run runs with no finish, or waits for a person.
 */
export function RunListDuration(run: RunTimes) {
  const live = !isUnsetTime(run.startedAt) && countsToNow(run);
  return (
    <>{useClock(live ? 1000 : null, (now) => runListDuration(run, now))}</>
  );
}

/** A running run with no finish, and a waiting run, count up to now: a waiting run has the finish time of its pause. */
function countsToNow({ finishedAt, status }: RunTimes): boolean {
  return (
    status === Status.Waiting ||
    (status === Status.Running && isUnsetTime(finishedAt))
  );
}

function runListDuration(run: RunTimes, now: number): string {
  if (isUnsetTime(run.startedAt)) {
    return '-';
  }
  const start = dayjs(run.startedAt);
  if (countsToNow(run)) {
    return formatDuration(dayjs(now).diff(start));
  }
  if (!isUnsetTime(run.finishedAt)) {
    return formatDuration(dayjs(run.finishedAt).diff(start));
  }
  return '-';
}

/** "Xs", "Xm Ys" or "Xh Ym Zs". */
function formatDuration(durationMs: number): string {
  const seconds = Math.floor(durationMs / 1000);
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = seconds % 60;
  if (minutes < 60) {
    return `${minutes}m ${remainingSeconds}s`;
  }
  const hours = Math.floor(minutes / 60);
  const remainingMinutes = minutes % 60;
  return `${hours}h ${remainingMinutes}m ${remainingSeconds}s`;
}
