// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { StoredDraft } from '../contracts';

function isStoredDraft(value: unknown): value is StoredDraft {
  if (typeof value !== 'object' || value === null) return false;
  const v = value as Record<string, unknown>;
  return (
    typeof v.text === 'string' &&
    typeof v.savedAt === 'number' &&
    Number.isFinite(v.savedAt) &&
    (v.baseSha256 === undefined || typeof v.baseSha256 === 'string')
  );
}

/** Reads a stored draft. Returns null when it is absent, corrupt or unreadable. */
export function readDraft(key: string): StoredDraft | null {
  try {
    const raw = localStorage.getItem(key);
    if (raw === null) return null;
    const parsed: unknown = JSON.parse(raw);
    return isStoredDraft(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

/** Stores a draft. Returns false when the browser refuses the write. */
export function writeDraft(key: string, draft: StoredDraft): boolean {
  try {
    localStorage.setItem(key, JSON.stringify(draft));
    return true;
  } catch {
    return false;
  }
}

/** Removes a stored draft. Never throws. */
export function removeDraft(key: string): void {
  try {
    localStorage.removeItem(key);
  } catch {
    // Storage is unavailable; there is nothing to remove.
  }
}
