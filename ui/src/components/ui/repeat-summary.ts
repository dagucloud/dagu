// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { formatDurationMs } from '@/lib/formatDuration';

/** The translator of static messages, as useI18n gives it. */
export type Translate = (
  source: string,
  values?: Record<string, string | number>
) => string;

/** The repeat policy of a step, in one shape for the YAML and for the API. */
export interface RepeatPolicyView {
  mode: 'while' | 'until';
  /** Seconds between two runs, when the policy sets a fixed number. */
  intervalSec?: number;
  /** The maximum number of runs, when the policy sets a fixed number. */
  limit?: number;
  /** The condition, with ` = expected` appended when the policy has one. */
  condition?: string;
  exitCodes?: number[];
  /** True when the interval grows after each run. */
  backoff?: boolean;
}

/** The longest condition that a summary shows; the title keeps all of it. */
export const REPEAT_CONDITION_MAX = 24;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** A positive whole number, written as a number or as digits; else undefined. */
function count(value: unknown): number | undefined {
  const n =
    typeof value === 'string' && /^\s*\d+\s*$/.test(value)
      ? Number(value)
      : value;
  return typeof n === 'number' && Number.isInteger(n) && n > 0 ? n : undefined;
}

function text(value: unknown): string {
  return typeof value === 'string' || typeof value === 'number'
    ? String(value).trim()
    : '';
}

function conditionOf(
  condition: unknown,
  expected: unknown
): string | undefined {
  // The API gives {condition, expected}; the YAML gives two strings.
  const source = isRecord(condition) ? condition.condition : condition;
  const wanted = isRecord(condition) ? condition.expected : expected;
  const base = text(source);
  if (!base) return undefined;
  const want = text(wanted);
  return want ? `${base} = ${want}` : base;
}

function exitCodesOf(value: unknown): number[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const codes = value.filter(
    (code): code is number => typeof code === 'number' && Number.isInteger(code)
  );
  return codes.length > 0 ? codes : undefined;
}

/**
 * Reads the repeat policy of a step: the `repeat_policy` block of the YAML
 * (`interval_sec`, `exit_code`) or the `repeatPolicy` of the API (`interval`,
 * `exitCode`). `repeat: until` is until; every other value of `repeat`, such
 * as the boolean of old files, is while. Without `repeat`, the server infers
 * the mode, and so does this function. Returns null when the step does not
 * repeat.
 */
export function readRepeatPolicy(raw: unknown): RepeatPolicyView | null {
  if (!isRecord(raw)) return null;
  const condition = conditionOf(raw.condition, raw.expected);
  const exitCodes = exitCodesOf(raw.exit_code ?? raw.exitCode);
  let mode: RepeatPolicyView['mode'] | null = null;
  const repeat = raw.repeat;
  if (repeat === 'until') {
    mode = 'until';
  } else if (repeat === 'while' || repeat === true) {
    mode = 'while';
  } else if (repeat !== undefined && repeat !== null && repeat !== false) {
    // An unknown form reads as while.
    mode = 'while';
  } else if (
    condition !== undefined &&
    text(isRecord(raw.condition) ? raw.condition.expected : raw.expected)
  ) {
    mode = 'until';
  } else if (condition !== undefined || exitCodes !== undefined) {
    mode = 'while';
  }
  if (mode === null) return null;
  const view: RepeatPolicyView = { mode };
  const intervalSec = count(
    raw.interval_sec ?? raw.intervalSec ?? raw.interval
  );
  if (intervalSec !== undefined) view.intervalSec = intervalSec;
  const limit = count(raw.limit);
  if (limit !== undefined) view.limit = limit;
  if (condition !== undefined) view.condition = condition;
  if (exitCodes !== undefined) view.exitCodes = exitCodes;
  if (raw.backoff !== undefined && raw.backoff !== false && raw.backoff !== 0) {
    view.backoff = true;
  }
  return view;
}

/** The condition cut to REPEAT_CONDITION_MAX characters, with an ellipsis. */
export function shortCondition(condition: string): string {
  return condition.length > REPEAT_CONDITION_MAX
    ? `${condition.slice(0, REPEAT_CONDITION_MAX - 1)}…`
    : condition;
}

/** "10s", "2m": the interval as the other durations of the UI read. */
export function repeatInterval(seconds: number): string {
  return formatDurationMs(seconds * 1000);
}

/**
 * The text of the repeat rule: what the step waits for, as in
 * `repeat while exit 0` or `repeat until "ready = yes"`. A policy with
 * neither condition nor exit codes repeats while, or until, the step exits 0.
 */
export function repeatRule(policy: RepeatPolicyView, ts: Translate): string {
  const until = policy.mode === 'until';
  if (policy.condition !== undefined) {
    return ts(
      until ? 'repeat until "{condition}"' : 'repeat while "{condition}"',
      {
        condition: shortCondition(policy.condition),
      }
    );
  }
  return ts(until ? 'repeat until exit {codes}' : 'repeat while exit {codes}', {
    codes: (policy.exitCodes ?? [0]).join(', '),
  });
}

/**
 * The one line that describes a repeat policy, for the card tooltip, the run
 * list and the step facts: `repeat while exit 0 · every 10s · no limit` or
 * `repeat until "ready = yes" · every 30s · max 5`.
 */
export function repeatSummary(policy: RepeatPolicyView, ts: Translate): string {
  const parts = [repeatRule(policy, ts)];
  if (policy.intervalSec !== undefined) {
    parts.push(
      ts('every {interval}', { interval: repeatInterval(policy.intervalSec) })
    );
  }
  parts.push(
    policy.limit !== undefined
      ? ts('max {limit}', { limit: policy.limit })
      : ts('no limit')
  );
  return parts.join(' · ');
}

/**
 * The iteration of a step in a run: `iteration 3 of 5` with a limit,
 * `iteration 3` without. `done` is the number of finished runs. A step that
 * runs is in the next iteration. Returns null before the first run. `format`
 * writes the number: the badge gives the whole count with the separators of
 * the language.
 */
export function repeatIteration(
  policy: RepeatPolicyView,
  done: number,
  running: boolean,
  ts: Translate,
  format: (n: number) => string = String
): string | null {
  const iteration = running ? done + 1 : done;
  if (iteration <= 0) return null;
  return policy.limit !== undefined
    ? ts('iteration {n} of {limit}', {
        n: format(iteration),
        limit: policy.limit,
      })
    : ts('iteration {n}', { n: format(iteration) });
}
