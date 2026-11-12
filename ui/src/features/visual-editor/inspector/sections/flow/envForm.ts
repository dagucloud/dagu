// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { DagEditOp, StepKey } from '../../../contracts';
import { diffFormData, toFieldOps } from '../../formDiff';

/**
 * Forms of a step `env`: a list of one-key maps (`- KEY: value`), a list
 * of `KEY=value` strings, a map, no value, or another shape that the
 * editor does not change.
 */
export type EnvForm = 'maps' | 'strings' | 'map' | 'none' | 'other';

export interface EnvModel {
  form: EnvForm;
  entries: Record<string, string>;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : JSON.stringify(value);
}

/** Reads the form and the entries of an `env` value. */
export function readEnv(value: unknown): EnvModel {
  if (value === undefined) {
    return { form: 'none', entries: {} };
  }
  if (isRecord(value)) {
    return {
      form: 'map',
      entries: Object.fromEntries(
        Object.entries(value).map(([k, v]) => [k, text(v)])
      ),
    };
  }
  if (!Array.isArray(value)) {
    return { form: 'other', entries: {} };
  }
  // No prototype: an entry named `__proto__` stays a key of its own.
  const entries: Record<string, string> = Object.create(null);
  if (value.every((item) => isRecord(item) && Object.keys(item).length === 1)) {
    for (const item of value as Record<string, unknown>[]) {
      const [key, v] = Object.entries(item)[0]!;
      entries[key] = text(v);
    }
    return { form: 'maps', entries };
  }
  if (
    value.every(
      (item) =>
        typeof item === 'string' && /^[A-Za-z_][A-Za-z0-9_]*=/.test(item)
    )
  ) {
    for (const item of value as string[]) {
      const at = item.indexOf('=');
      entries[item.slice(0, at)] = item.slice(at + 1);
    }
    return { form: 'strings', entries };
  }
  return { form: 'other', entries: {} };
}

/**
 * The position of the item that `readEnv` reads a key from, in a list of
 * one-key maps: the last item with the key, as a later entry wins.
 */
function itemOf(
  items: readonly Record<string, unknown>[],
  key: string
): number {
  for (let i = items.length - 1; i >= 0; i--) {
    if (Object.keys(items[i]!)[0] === key) return i;
  }
  return -1;
}

function sameKeys(
  a: Record<string, string>,
  b: Record<string, string>
): boolean {
  const left = Object.keys(a);
  const right = Object.keys(b);
  return (
    left.length === right.length && left.every((key, i) => right[i] === key)
  );
}

/**
 * Edits that turn the `env` of a step into `next`, in the form that the
 * step uses. Value changes of a list of maps give one edit per item key;
 * a map gives one edit per key; other changes replace the list.
 */
export function envOps(
  step: StepKey,
  value: unknown,
  next: Record<string, string>
): DagEditOp[] {
  const current = readEnv(value);
  const replace = (v: unknown): DagEditOp[] => [
    { kind: 'setStepField', step, path: ['env'], value: v },
  ];
  switch (current.form) {
    case 'map':
      return toFieldOps(step, diffFormData(current.entries, next, ['env']));
    case 'maps':
      if (sameKeys(current.entries, next)) {
        const items = value as Record<string, unknown>[];
        return Object.keys(next).flatMap((key) =>
          current.entries[key] === next[key]
            ? []
            : [
                {
                  kind: 'setStepField',
                  step,
                  path: ['env', itemOf(items, key), key],
                  value: next[key],
                },
              ]
        );
      }
      return replace(
        Object.keys(next).length > 0
          ? Object.entries(next).map(([key, v]) => ({ [key]: v }))
          : undefined
      );
    case 'strings':
      return replace(
        Object.keys(next).length > 0
          ? Object.entries(next).map(([key, v]) => `${key}=${v}`)
          : undefined
      );
    case 'none':
      return Object.keys(next).length > 0
        ? replace(Object.entries(next).map(([key, v]) => ({ [key]: v })))
        : [];
    default:
      return [];
  }
}
