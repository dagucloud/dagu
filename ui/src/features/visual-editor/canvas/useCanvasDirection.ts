// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useSyncExternalStore } from 'react';
import type { CanvasDirection } from '../contracts';

/**
 * Storage key of the default direction: the one of a canvas whose workflow
 * has no direction of its own.
 */
export const CANVAS_DIRECTION_KEY = 'dagu.canvas.direction';

/** Storage key of the direction that the user chose for one workflow. */
export function canvasDirectionKey(scope: string): string {
  return `${CANVAS_DIRECTION_KEY}:${scope}`;
}

/** Cookie of the direction of the Mermaid graph: 'LR' or 'TD'. */
const FLOWCHART_COOKIE = 'flowchart';

const DEFAULT_DIRECTION: CanvasDirection = 'TB';

const listeners = new Set<() => void>();

// The value of the last change of each key when the storage refused it, so
// that the canvases still agree while the page lives.
const unsaved = new Map<string, CanvasDirection>();

function asDirection(value: unknown): CanvasDirection | null {
  return value === 'LR' || value === 'TB' ? value : null;
}

function readStored(key: string): CanvasDirection | null {
  const kept = unsaved.get(key);
  if (kept) {
    // A refused write can leave an older stored value: the last change wins.
    return kept;
  }
  try {
    return asDirection(window.localStorage.getItem(key));
  } catch {
    return null;
  }
}

function writeStored(key: string, direction: CanvasDirection): void {
  try {
    window.localStorage.setItem(key, direction);
    unsaved.delete(key);
  } catch {
    unsaved.set(key, direction);
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

function defaultDirection(): CanvasDirection {
  const stored = readStored(CANVAS_DIRECTION_KEY);
  if (stored) {
    return stored;
  }
  const seeded = cookieDirection();
  if (seeded) {
    // Seed once: the stored value wins over later changes of the cookie.
    writeStored(CANVAS_DIRECTION_KEY, seeded);
    return seeded;
  }
  return DEFAULT_DIRECTION;
}

function currentDirection(scope: string | undefined): CanvasDirection {
  return (
    (scope === undefined ? null : readStored(canvasDirectionKey(scope))) ??
    defaultDirection()
  );
}

function setDirection(scope: string | undefined, direction: CanvasDirection) {
  writeStored(
    scope === undefined ? CANVAS_DIRECTION_KEY : canvasDirectionKey(scope),
    direction
  );
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
 * The direction of the canvas layout of a workflow (`scope`). A change for
 * one workflow does not turn the others: it writes the key of the workflow.
 * A workflow without a direction of its own, and a canvas without a scope,
 * read the default: localStorage[CANVAS_DIRECTION_KEY], which is seeded one
 * time from the cookie 'flowchart' of the Mermaid graph ('LR' gives 'LR',
 * 'TD' gives 'TB'), else 'TB'. A change updates every canvas of the same
 * scope. Storage errors are ignored.
 */
export function useCanvasDirection(
  scope?: string
): [CanvasDirection, (direction: CanvasDirection) => void] {
  const direction = useSyncExternalStore(
    subscribe,
    () => currentDirection(scope),
    () => DEFAULT_DIRECTION
  );
  const set = useCallback(
    (next: CanvasDirection) => setDirection(scope, next),
    [scope]
  );
  return [direction, set];
}
