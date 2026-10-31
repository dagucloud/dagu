// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { TextHistory } from '../contracts';

/**
 * Creates a snapshot history of editor text. It keeps at most `limit` undo
 * steps and drops the oldest one first. A push with the same `coalesceKey`
 * as the previous push replaces the newest snapshot. `undo` and `redo` end
 * the chain.
 */
export function createTextHistory(
  initial: string,
  limit: number = 100
): TextHistory {
  const maxUndo = Math.max(1, Math.floor(limit));
  let entries: string[] = [initial];
  let pos = 0;
  let lastKey: string | undefined;

  return {
    push(text: string, coalesceKey?: string): void {
      if (text === entries[pos]) return;
      entries.length = pos + 1;
      if (coalesceKey !== undefined && coalesceKey === lastKey && pos > 0) {
        entries[pos] = text;
        return;
      }
      lastKey = coalesceKey;
      entries.push(text);
      pos++;
      if (entries.length > maxUndo + 1) {
        entries.shift();
        pos--;
      }
    },
    undo(): string | null {
      lastKey = undefined;
      if (pos === 0) return null;
      pos--;
      return entries[pos] ?? null;
    },
    redo(): string | null {
      lastKey = undefined;
      if (pos >= entries.length - 1) return null;
      pos++;
      return entries[pos] ?? null;
    },
    canUndo: () => pos > 0,
    canRedo: () => pos < entries.length - 1,
    reset(text: string): void {
      entries = [text];
      pos = 0;
      lastKey = undefined;
    },
  };
}
