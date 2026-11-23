// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { repeatInterval, type Translate } from '@/components/ui/repeat-summary';
import { isRecord } from '../record';

/**
 * A number, or the text that the server resolves when the step runs: a
 * `${...}` reference or a variable. A string of digits is its number.
 */
export function numberOrReference(value: unknown): number | string | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value !== 'string') return undefined;
  const text = value.trim();
  if (text === '') return undefined;
  return /^\d+$/.test(text) ? Number(text) : text;
}

/** The retry policy of a step, as the card shows it. */
export interface RetryFacts {
  limit?: number | string;
  intervalSec?: number | string;
  /** True when the wait grows after each attempt. */
  backoff?: boolean;
  exitCodes?: number[];
}

/** Reads the `retry_policy` of a step. Undefined when the step has none that says anything. */
export function readRetry(value: unknown): RetryFacts | undefined {
  if (!isRecord(value)) return undefined;
  const retry: RetryFacts = {};
  const limit = numberOrReference(value.limit);
  if (limit !== undefined) retry.limit = limit;
  const interval = numberOrReference(value.interval_sec);
  if (interval !== undefined) retry.intervalSec = interval;
  if (value.backoff !== undefined && value.backoff !== false) {
    retry.backoff = true;
  }
  const raw = value.exit_code;
  const codes = (Array.isArray(raw) ? raw : [raw]).filter(
    (code): code is number => typeof code === 'number'
  );
  if (codes.length > 0) retry.exitCodes = codes;
  return Object.keys(retry).length === 0 ? undefined : retry;
}

/** A duration in seconds as the UI reads it: "5m"; a reference stays as written. */
export function secondsText(value: number | string): string {
  return typeof value === 'number' ? repeatInterval(value) : value;
}

/** The whole retry policy as sentences, for a tooltip. */
export function retryTitle(ts: Translate, retry: RetryFacts): string {
  return [
    retry.limit !== undefined
      ? ts('Retries the step up to {limit} times.', { limit: retry.limit })
      : ts('Retries the step.'),
    retry.intervalSec !== undefined
      ? ts(
          retry.backoff
            ? 'Waits {interval} before the second attempt, then longer each time.'
            : 'Waits {interval} between the attempts.',
          { interval: secondsText(retry.intervalSec) }
        )
      : '',
    retry.exitCodes
      ? ts('Retries only after the exit code {codes}.', {
          codes: retry.exitCodes.join(', '),
        })
      : '',
  ]
    .filter(Boolean)
    .join('\n');
}
