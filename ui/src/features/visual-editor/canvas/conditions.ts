// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { CanvasEdge, ContinueOnView, EdgeCondition } from '../contracts';
import { isRecord, type Rec } from '../record';

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== ''
    ? value.trim()
    : undefined;
}

/** An exit code as the server reads it: a number, or a string of digits. */
function exitCodeOf(value: unknown): number | undefined {
  if (typeof value === 'number') return value;
  const digits = typeof value === 'string' ? value.trim() : '';
  return /^\d+$/.test(digits) ? Number(digits) : undefined;
}

/**
 * Reads `continue_on` of a step as the server does: the shorthand `failed` or
 * `skipped`, or the map. It never throws. A value that lets the run carry on
 * after nothing gives undefined.
 */
export function readContinueOn(value: unknown): ContinueOnView | undefined {
  const view: ContinueOnView = {};
  const shorthand =
    typeof value === 'string' ? value.trim().toLowerCase() : undefined;
  if (shorthand === 'failed') view.failure = true;
  else if (shorthand === 'skipped') view.skipped = true;
  else if (isRecord(value)) {
    if (value.failure === true || value.failed === true) view.failure = true;
    if (value.skipped === true) view.skipped = true;
    const codes = (
      Array.isArray(value.exit_code) ? value.exit_code : [value.exit_code]
    )
      .map(exitCodeOf)
      .filter((code): code is number => code !== undefined);
    if (codes.length > 0) view.exitCodes = codes;
    const output = Array.isArray(value.output) ? value.output : [value.output];
    if (output.some((item) => text(item) !== undefined)) view.output = true;
    // `mark_success` alone changes nothing: it needs a cause.
    if (Object.keys(view).length > 0 && value.mark_success === true) {
      view.markSuccess = true;
    }
  }
  return Object.keys(view).length === 0 ? undefined : view;
}

/** The label of the continue mark: "continues on failure", one phrase for each cause. */
export function continueLabel(
  ts: (text: string, values?: Record<string, string | number>) => string,
  view: ContinueOnView
): string {
  const causes: string[] = [];
  if (view.failure) causes.push(ts('continues on failure'));
  if (view.skipped) causes.push(ts('continues if skipped'));
  if (view.exitCodes) {
    causes.push(
      ts('continues on exit code {codes}', { codes: view.exitCodes.join(', ') })
    );
  }
  if (view.output) causes.push(ts('continues on matching output'));
  return causes.join(' · ');
}

/** One precondition of a step: the text and the parts that an edge reads. */
export interface PreconditionView {
  /** One line: the condition, then the comparison with `expected`. */
  text: string;
  condition?: string;
  expected?: string;
  negate: boolean;
}

function preconditionOf(item: unknown): PreconditionView | null {
  const plain = text(item);
  if (plain !== undefined) return { text: plain, negate: false };
  if (!isRecord(item)) return null;
  const condition = text(item.condition) ?? text(item.eval);
  if (condition === undefined) return null;
  const expected = text(item.expected);
  const negate = item.negate === true;
  const view: PreconditionView = {
    text:
      expected === undefined
        ? negate
          ? `not ${condition}`
          : condition
        : `${condition} ${negate ? '!=' : '='} ${expected}`,
    condition,
    negate,
  };
  if (expected !== undefined) view.expected = expected;
  return view;
}

/** Reads `preconditions` of a step: a command, a map, or a list of them. It never throws. */
export function readPreconditions(step: Rec): PreconditionView[] {
  const raw = step.preconditions;
  const list = Array.isArray(raw) ? raw : raw === undefined ? [] : [raw];
  return list.flatMap((item) => {
    const view = preconditionOf(item);
    return view ? [view] : [];
  });
}

const REFERENCE = /\$\{\s*(?:steps\.)?([^.}\s]+)\.([^}\s]+)\s*\}/g;

/**
 * The condition that the preconditions of a step put on the link from the
 * step `from`: the first precondition that reads `${from.field}`. `from` is
 * every name that the YAML can call the source: its name, its id. A test of
 * `exit_code` against 0 reads as "failed" or "succeeded", which is the
 * shape of a verify loop. Undefined when no precondition reads the source.
 */
export function edgeCondition(
  preconditions: readonly PreconditionView[],
  from: readonly string[],
  label: string
): EdgeCondition | undefined {
  for (const pre of preconditions) {
    for (const match of (pre.condition ?? '').matchAll(REFERENCE)) {
      const [, ref, field] = match;
      if (ref === undefined || field === undefined || !from.includes(ref)) {
        continue;
      }
      const base = { step: label, negate: pre.negate, text: pre.text };
      const expected = pre.expected;
      if (field === 'exit_code' && expected === '0') {
        return { ...base, when: pre.negate ? 'failed' : 'succeeded' };
      }
      if (
        field === 'exit_code' &&
        expected !== undefined &&
        /^\d+$/.test(expected)
      ) {
        return { ...base, when: 'exit', value: expected };
      }
      return {
        ...base,
        when: 'match',
        value:
          expected === undefined
            ? field
            : `${field} ${pre.negate ? '!=' : '='} ${expected}`,
      };
    }
  }
  return undefined;
}

/** True for an edge that a condition can sit on: a link of `depends`. */
export function carriesCondition(edge: Pick<CanvasEdge, 'kind'>): boolean {
  return edge.kind === 'depends';
}
