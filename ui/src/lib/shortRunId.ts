// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** The characters of a run ID that the UI shows where the whole ID is too long. */
export const SHORT_RUN_ID_LENGTH = 8;

/** The IDs of the scheduler: `<scheme>-<hash>` and `<scheme>-<hash>-<time>`. */
const SCHEDULER_ID =
  /^(?:sched|restart|catchup|oneoff)-([0-9a-f]{6,})(?:-\d{4}(\d{4})T(\d{6}))?$/;

/**
 * The one short form of a run ID: its first 8 characters. Show the whole ID
 * as the title of the element that carries the short form. An ID of the
 * scheduler starts with the same scheme for every run, and most schemes hash
 * the same value for every run of a DAG, so its short form is the date and
 * time of the ID (MMDD-HHMMSS, UTC), as in `1108-150405`.
 */
export function shortRunId(id: string): string {
  const scheduled = SCHEDULER_ID.exec(id);
  if (scheduled) {
    const [, hash = '', date, time] = scheduled;
    return date && time
      ? `${date}-${time}`
      : hash.slice(0, SHORT_RUN_ID_LENGTH);
  }
  return id.slice(0, SHORT_RUN_ID_LENGTH);
}
