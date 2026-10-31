// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useSyncExternalStore } from 'react';
import type { CanvasDirection } from '../contracts';

/** Storage key of the direction that all canvases share. */
export const CANVAS_DIRECTION_KEY = 'dagu.canvas.direction';

/** Cookie of the direction of the Mermaid graph: 'LR' or 'TD'. */
const FLOWCHART_COOKIE = 'flowchart';

const DEFAULT_DIRECTION: CanvasDirection = 'TB';

const listeners = new Set<() => void>();

// The value of the last change when the storage refused it, so that the
// canvases still agree while the page lives.
let unsaved: CanvasDirection | null = null;

function asDirection(value: unknown): CanvasDirection | null {
  return value === 'LR' || value === 'TB' ? value : null;
}

function readStored(): CanvasDirection | null {
  try {
    return asDirection(window.localStorage.getItem(CANVAS_DIRECTION_KEY));
  } catch {
    return null;
  }
}

function writeStored(direction: CanvasDirection): boolean {
  try {
    window.localStorage.setItem(CANVAS_DIRECTION_KEY, direction);
    return true;
  } catch {
    return false;
  }
}

function cookieDirection(): CanvasDirection | null {
  let cookies: string;
  try {
    cookies = document.cookie;
  } catch {
    return null;
  }
  for (const part of cookies.split(';')) {
    const at = part.indexOf('=');
    if (at === -1 || part.slice(0, at).trim() !== FLOWCHART_COOKIE) {
      continue;
    }
    let value = part.slice(at + 1).trim();
    try {
      value = decodeURIComponent(value);
    } catch {
      return null;
    }
    if (value === 'LR') {
      return 'LR';
    }
    return value === 'TD' ? 'TB' : null;
  }
  return null;
}

function currentDirection(): CanvasDirection {
  // A refused write can leave an older stored value: the last change wins.
  if (unsaved) {
    return unsaved;
  }
  const stored = readStored();
  if (stored) {
    return stored;
  }
  const seeded = cookieDirection();
  if (seeded) {
    // Seed once: the stored value wins over later changes of the cookie.
    writeStored(seeded);
    return seeded;
  }
  return DEFAULT_DIRECTION;
}

function setDirection(direction: CanvasDirection) {
  unsaved = writeStored(direction) ? null : direction;
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
 * The direction of the canvas layout, shared by all canvases. It reads
 * localStorage[CANVAS_DIRECTION_KEY]. When the key is absent, it seeds the
 * key one time from the cookie 'flowchart' of the Mermaid graph ('LR' gives
 * 'LR', 'TD' gives 'TB'). The default is 'TB'. A change writes the key and
 * updates every canvas. Storage errors are ignored.
 */
export function useCanvasDirection(): [
  CanvasDirection,
  (direction: CanvasDirection) => void,
] {
  const direction = useSyncExternalStore(
    subscribe,
    currentDirection,
    () => DEFAULT_DIRECTION
  );
  return [direction, setDirection];
}
