// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useSyncExternalStore } from 'react';
import { writeLocalStorage } from '@/lib/local-storage-migration';

// Components reading the same key in this tab; the storage event covers
// other tabs only.
const listeners = new Map<string, Set<() => void>>();
// Values the browser refused to store, kept for the life of the page.
const unstored = new Map<string, string>();

function read(key: string): string | null {
  const kept = unstored.get(key);
  if (kept !== undefined) return kept;
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function subscribe(key: string, onChange: () => void): () => void {
  const keyListeners = listeners.get(key) ?? new Set();
  keyListeners.add(onChange);
  listeners.set(key, keyListeners);
  const onStorage = (event: StorageEvent) => {
    if (event.key === key || event.key === null) {
      unstored.delete(key);
      onChange();
    }
  };
  window.addEventListener('storage', onStorage);
  return () => {
    keyListeners.delete(onChange);
    if (keyListeners.size === 0) listeners.delete(key);
    window.removeEventListener('storage', onStorage);
  };
}

/**
 * Returns the browser-stored value of a key, or null when it is unset, with
 * a setter. Every component reading the key sees a new value at once.
 */
export function useStoredValue(
  key: string
): [string | null, (value: string) => void] {
  const value = useSyncExternalStore(
    useCallback((onChange) => subscribe(key, onChange), [key]),
    () => read(key),
    () => null
  );
  const setValue = useCallback(
    (next: string) => {
      if (writeLocalStorage(key, next)) unstored.delete(key);
      else unstored.set(key, next);
      listeners.get(key)?.forEach((onChange) => onChange());
    },
    [key]
  );
  return [value, setValue];
}
