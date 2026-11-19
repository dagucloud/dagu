// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** A YAML map as the plain value of the parser gives it. */
export type Rec = Record<string, unknown>;

/** True for a YAML map: an object that is not null and not a list. */
export function isRecord(value: unknown): value is Rec {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
