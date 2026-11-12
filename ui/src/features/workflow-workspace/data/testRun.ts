// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ApiDAGRunDetails } from './types';

/**
 * How long the server keeps a test run after its end: 10 minutes. The same
 * number is `EphemeralRunTTL` of the server (`internal/persis`); the UI reads
 * it from here only, to say when the run disappears. The server looks for
 * runs to remove once a minute, so the run can stay up to a minute longer.
 */
export const TEST_RUN_TTL_MS = 10 * 60 * 1000;

/** The minutes of `TEST_RUN_TTL_MS`, for the texts. */
export const TEST_RUN_TTL_MINUTES = TEST_RUN_TTL_MS / 60_000;

/**
 * When the server removes a test run, in ms since the epoch: its end plus the
 * TTL. null while the run has not ended or when it has no end time.
 */
export function testRunExpiry(
  run: Pick<ApiDAGRunDetails, 'finishedAt'> | null | undefined
): number | null {
  const value = run?.finishedAt;
  if (!value || value === '-' || value.startsWith('0001-01-01')) return null;
  const end = Date.parse(value);
  return Number.isFinite(end) ? end + TEST_RUN_TTL_MS : null;
}
