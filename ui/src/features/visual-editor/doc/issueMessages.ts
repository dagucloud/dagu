// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** Messages of the client issues that a quick fix answers. */
export const NO_ID_MESSAGE = 'The step has no id, as the other steps have.';
export const OUTPUTS_ID_MESSAGE =
  'id is required when the step declares outputs.';

/**
 * The message of a field that accepts one value only: `with.read must be
 * true.` A quick fix reads the value back with `mustBeValue`.
 */
export function mustBeMessage(where: string, value: unknown): string {
  return `${where} must be ${JSON.stringify(value)}.`;
}

// A path, then a JSON scalar: a string, a number, true, false or null.
const MUST_BE =
  /^\S+ must be (true|false|null|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|"(?:[^"\\]|\\.)*")\.$/;

/**
 * The value that a `mustBeMessage` names, or undefined for any other
 * message. The value is a string, a number, a boolean or null.
 */
export function mustBeValue(
  message: string
): { value: string | number | boolean | null } | undefined {
  const text = MUST_BE.exec(message)?.[1];
  if (text === undefined) return undefined;
  try {
    return { value: JSON.parse(text) as string | number | boolean | null };
  } catch {
    return undefined;
  }
}
