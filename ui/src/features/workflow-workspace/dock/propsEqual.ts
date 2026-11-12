// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

function plain(value: unknown): value is Record<string, unknown> {
  return (
    typeof value === 'object' &&
    value !== null &&
    (Array.isArray(value) || Object.getPrototypeOf(value) === Object.prototype)
  );
}

function sameEntries(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) {
    return false;
  }
  return a.every((item, i) => Object.is(item, b[i]));
}

/**
 * Two plain objects or arrays of the same kind with the same entries. An
 * entry that is an array of values equals an array with the same values.
 */
function shallowEqual(a: object, b: object): boolean {
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const left = a as Record<string, unknown>;
  const right = b as Record<string, unknown>;
  const keys = Object.keys(left);
  if (keys.length !== Object.keys(right).length) return false;
  return keys.every(
    (key) =>
      Object.prototype.hasOwnProperty.call(right, key) &&
      sameEntries(left[key], right[key])
  );
}

/**
 * The comparison for React.memo of a row that a parent rebuilds on each
 * render. A prop is equal when it is the same value, or when it is a plain
 * object or array with the same entries (see shallowEqual). Functions compare
 * by identity: the parent gives stable ones.
 */
export function propsEqual<P extends object>(prev: P, next: P): boolean {
  if (prev === next) return true;
  const left = prev as Record<string, unknown>;
  const right = next as Record<string, unknown>;
  const keys = Object.keys(left);
  if (keys.length !== Object.keys(right).length) return false;
  return keys.every((key) => {
    const a = left[key];
    const b = right[key];
    if (Object.is(a, b)) return true;
    return plain(a) && plain(b) && shallowEqual(a, b);
  });
}
