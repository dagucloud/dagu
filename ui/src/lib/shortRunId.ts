// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** The length of the short form of an ordinary run ID. */
export const SHORT_RUN_ID_LENGTH = 8;

/**
 * The IDs of the scheduler: `<scheme>-<hash>` and
 * `<scheme>-<hash>-<YYYYMMDD>T<HHMMSS>`, with the time in UTC.
 */
const SCHEDULER_ID =
  /^(?:sched|restart|catchup|oneoff)-([0-9a-f]{6,})(?:-\d{4}(\d{4}T\d{6}))?$/;

/**
 * The short form of a run ID, shown where the whole ID is too long: its first
 * 8 characters. Show the whole ID as the title of the element that carries
 * the short form. An ID of the scheduler starts with the same scheme for
 * every run, and the runs of one DAG can share its hash, so its short form is
 * the date and time of the ID without the year, as in `1108T150405`. A
 * scheduler ID without a time shows the start of its hash.
 */
export function shortRunId(id: string): string {
  const scheduled = SCHEDULER_ID.exec(id);
  if (scheduled) {
    const [, hash = '', time] = scheduled;
    return time ?? hash.slice(0, SHORT_RUN_ID_LENGTH);
  }
  return id.slice(0, SHORT_RUN_ID_LENGTH);
}
