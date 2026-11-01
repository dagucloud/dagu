// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useSyncExternalStore } from 'react';

/** Storage key of the choice that all canvases share: '1' shows, '0' hides. */
export const CANVAS_DETAILS_KEY = 'dagu.canvas.details';

const listeners = new Set<() => void>();

// The value of the last change when the storage refused it, so that the
// canvases still agree while the page lives.
let unsaved: boolean | null = null;

function readStored(): boolean | null {
  try {
    const value = window.localStorage.getItem(CANVAS_DETAILS_KEY);
    return value === '1' ? true : value === '0' ? false : null;
  } catch {
    return null;
  }
}

function currentValue(): boolean {
  return unsaved ?? readStored() ?? true;
}

function setValue(show: boolean) {
  try {
    window.localStorage.setItem(CANVAS_DETAILS_KEY, show ? '1' : '0');
    unsaved = null;
  } catch {
    unsaved = show;
  }
  for (const listener of listeners) {
    listener();
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/**
 * Whether the step cards show their name, id and action. When false, they
 * show only their icon. The default is true. The choice is kept in
 * localStorage[CANVAS_DETAILS_KEY] and shared by all canvases. Storage errors
 * are ignored.
 */
export function useCanvasDetails(): [boolean, (show: boolean) => void] {
  const show = useSyncExternalStore(subscribe, currentValue, () => true);
  return [show, setValue];
}
