// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useSyncExternalStore } from 'react';
import type { CacheScope, StorageLike } from './outputCache';

/**
 * The run that a step run takes the outputs of the previous steps from:
 * the automatic choice (`pickReuseSource`), one named run, or the cache of
 * the browser (values that were read from named runs).
 */
export type SourceChoice =
  | { kind: 'auto' }
  | { kind: 'cache' }
  | { kind: 'run'; runId: string };

export const AUTO_CHOICE: SourceChoice = { kind: 'auto' };

const RUN_PREFIX = 'run:';

/** The text that stands for a choice in the storage. */
export function serializeChoice(choice: SourceChoice): string {
  return choice.kind === 'run' ? `${RUN_PREFIX}${choice.runId}` : choice.kind;
}

/** The choice that a stored text stands for. An unknown text is the automatic choice. */
export function parseChoice(raw: string | null | undefined): SourceChoice {
  if (raw === 'cache') return { kind: 'cache' };
  if (raw?.startsWith(RUN_PREFIX) && raw.length > RUN_PREFIX.length) {
    return { kind: 'run', runId: raw.slice(RUN_PREFIX.length) };
  }
  return AUTO_CHOICE;
}

export interface SourceChoiceStore {
  /** The stored text of a workflow: 'auto', 'cache' or 'run:<id>'. */
  get(scope: CacheScope): string;
  set(scope: CacheScope, choice: SourceChoice): void;
  subscribe(listener: () => void): () => void;
}

const KEY_PREFIX = 'dagu.run-source.v1:';

/**
 * The choice of each workflow for the session. The choice stays in memory, so
 * it works when the storage is absent or blocked; each failure of the storage
 * is silent.
 */
export function createSourceChoiceStore(
  storage: () => StorageLike | null
): SourceChoiceStore {
  const memory = new Map<string, string>();
  const listeners = new Set<() => void>();
  const key = (scope: CacheScope) =>
    `${KEY_PREFIX}${scope.remoteNode}:${scope.fileName}`;
  const memoryKey = (scope: CacheScope) =>
    `${scope.remoteNode}\u0000${scope.fileName}`;
  function attempt<T>(use: (s: StorageLike) => T): T | undefined {
    try {
      const s = storage();
      return s ? use(s) : undefined;
    } catch {
      return undefined;
    }
  }
  return {
    get(scope) {
      const held = memory.get(memoryKey(scope));
      if (held !== undefined) return held;
      const loaded = serializeChoice(
        parseChoice(attempt((s) => s.getItem(key(scope))) ?? null)
      );
      memory.set(memoryKey(scope), loaded);
      return loaded;
    },
    set(scope, choice) {
      const text = serializeChoice(choice);
      if (memory.get(memoryKey(scope)) === text) return;
      memory.set(memoryKey(scope), text);
      attempt((s) => {
        if (text === 'auto') s.removeItem(key(scope));
        else s.setItem(key(scope), text);
      });
      listeners.forEach((listener) => listener());
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
}

/** The store of the page, on `sessionStorage`. */
export const sourceChoiceStore: SourceChoiceStore = createSourceChoiceStore(
  () => (typeof window === 'undefined' ? null : window.sessionStorage)
);

/** The choice of a workflow and its setter; the component renders again on a change. */
export function useSourceChoice(
  scope: CacheScope
): [SourceChoice, (choice: SourceChoice) => void] {
  const snapshot = useCallback(() => sourceChoiceStore.get(scope), [scope]);
  const text = useSyncExternalStore(
    sourceChoiceStore.subscribe,
    snapshot,
    snapshot
  );
  const set = useCallback(
    (choice: SourceChoice) => sourceChoiceStore.set(scope, choice),
    [scope]
  );
  return [parseChoice(text), set];
}
