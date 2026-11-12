// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { DagEditOp, StepKey } from '../contracts';
import type { FieldChange, FieldPath } from './types';

type Plain = Record<string, unknown>;

function isPlainObject(value: unknown): value is Plain {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** The value of an own key; a key such as `constructor` is not inherited. */
function ownValue(object: Plain, key: string): unknown {
  return Object.prototype.hasOwnProperty.call(object, key)
    ? object[key]
    : undefined;
}

/** Deep equality of JSON-like values; `undefined` keys count as absent. */
export function sameValue(a: unknown, b: unknown): boolean {
  if (a === b) {
    return true;
  }
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((item, i) => sameValue(item, b[i]));
  }
  if (isPlainObject(a) && isPlainObject(b)) {
    const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
    for (const key of keys) {
      if (!sameValue(ownValue(a, key), ownValue(b, key))) {
        return false;
      }
    }
    return true;
  }
  return false;
}

/**
 * Compares two form values and gives one change for each changed leaf.
 *
 * - Plain objects compare key by key: a new or changed key gives its new
 *   value, a removed key gives `value: undefined`.
 * - Arrays of the same length compare item by item.
 * - Arrays of different lengths, and a change of kind (object, array,
 *   scalar), give one change for the whole value.
 * - `undefined` and an absent key are equal.
 */
export function diffFormData(
  prev: unknown,
  next: unknown,
  base: FieldPath = []
): FieldChange[] {
  if (sameValue(prev, next)) {
    return [];
  }
  if (isPlainObject(prev) && isPlainObject(next)) {
    const changes: FieldChange[] = [];
    const keys = [
      ...Object.keys(prev),
      ...Object.keys(next).filter(
        (k) => !Object.prototype.hasOwnProperty.call(prev, k)
      ),
    ];
    for (const key of keys) {
      changes.push(
        ...diffFormData(ownValue(prev, key), ownValue(next, key), [
          ...base,
          key,
        ])
      );
    }
    return changes;
  }
  if (
    Array.isArray(prev) &&
    Array.isArray(next) &&
    prev.length === next.length
  ) {
    return prev.flatMap((item, index) =>
      diffFormData(item, next[index], [...base, index])
    );
  }
  return [{ path: base, value: next }];
}

/** One `setStepField` operation for each change. */
export function toFieldOps(step: StepKey, changes: FieldChange[]): DagEditOp[] {
  return changes.map((change) => ({
    kind: 'setStepField',
    step,
    path: change.path,
    value: change.value,
  }));
}
