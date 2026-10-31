// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** The characters of a run ID that the UI shows where the whole ID is too long. */
export const SHORT_RUN_ID_LENGTH = 8;

/**
 * The one short form of a run ID: its first 8 characters. Show the whole ID
 * as the title of the element that carries the short form.
 */
export function shortRunId(id: string): string {
  return id.slice(0, SHORT_RUN_ID_LENGTH);
}
