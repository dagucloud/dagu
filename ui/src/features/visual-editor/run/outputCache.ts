// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * What the browser remembers of the upstream steps: for each step, a
 * reference to a run in which it succeeded (`runId`, `capturedAt`) and the
 * hash of the step definition (see `stepHashes`). A step run names that run
 * to the server (`outputsFromRuns`), which reads the outputs from it: the
 * browser never sends a value. The values that were read from the run stay
 * for display only, labelled with their run; nobody can type or edit one.
 */
export interface CacheEntry {
  /**
   * Output values by output name, for display only: they are never sent. A
   * JSON value is its compact JSON text.
   */
  values: Record<string, string>;
  /** The run that made the values. Never empty. */
  runId: string;
  /** Capture time, in ms since the epoch. */
  capturedAt: number;
  /** Hash of the step and of the steps that it needs, when it was captured. */
  stepHash: string;
}

/** The entries of one workflow, by server step name. */
export type CacheEntries = Readonly<Record<string, CacheEntry>>;

/** The remote node and the workflow file that own a set of entries. */
export interface CacheScope {
  remoteNode: string;
  fileName: string;
}

/** The most bytes of one value. A larger value is not cached. */
export const MAX_VALUE_BYTES = 64 * 1024;
/** The most bytes that the entries of one workflow take. */
export const MAX_WORKFLOW_BYTES = 200 * 1024;

const encoder = typeof TextEncoder === 'undefined' ? null : new TextEncoder();

function utf8Length(text: string): number {
  return encoder ? encoder.encode(text).length : text.length;
}

/** The bytes that an entry takes in the storage. */
export function entryBytes(step: string, entry: CacheEntry): number {
  return utf8Length(JSON.stringify([step, entry]));
}

/** True when the values are not empty and none is larger than the limit. */
export function isCacheable(values: Readonly<Record<string, string>>): boolean {
  const names = Object.keys(values);
  return (
    names.length > 0 &&
    names.every((name) => utf8Length(values[name] as string) <= MAX_VALUE_BYTES)
  );
}

function totalBytes(entries: CacheEntries): number {
  return Object.entries(entries).reduce(
    (sum, [step, entry]) => sum + entryBytes(step, entry),
    0
  );
}

/**
 * Puts an entry in a set of entries, and answers the new set. The set does
 * not change, and `stored` is false, when the entry has a value over the
 * limit, when it names no run, or when it cannot fit in the cap of the
 * workflow. To make room, the oldest entries go first.
 */
export function putEntry(
  entries: CacheEntries,
  step: string,
  entry: CacheEntry
): { entries: CacheEntries; stored: boolean } {
  const refused = { entries, stored: false };
  if (!isCacheable(entry.values) || entry.runId === '') return refused;
  const next: Record<string, CacheEntry> = { ...entries, [step]: entry };
  let total = totalBytes(next);
  if (total > MAX_WORKFLOW_BYTES) {
    const oldest = Object.entries(next)
      .filter(([name]) => name !== step)
      .sort(([, a], [, b]) => a.capturedAt - b.capturedAt);
    for (const [name] of oldest) {
      if (total <= MAX_WORKFLOW_BYTES) break;
      total -= entryBytes(name, next[name] as CacheEntry);
      delete next[name];
    }
    if (total > MAX_WORKFLOW_BYTES) return refused;
  }
  return { entries: next, stored: true };
}

/** The set without the entry of a step. The same set when it has none. */
export function removeEntry(entries: CacheEntries, step: string): CacheEntries {
  if (!(step in entries)) return entries;
  const next = { ...entries };
  delete next[step];
  return next;
}

/** True when two entries hold the same values for the same run and step hash. */
export function sameEntry(a: CacheEntry | undefined, b: CacheEntry): boolean {
  if (!a) return false;
  const left = Object.keys(a.values);
  return (
    a.runId === b.runId &&
    a.stepHash === b.stepHash &&
    left.length === Object.keys(b.values).length &&
    left.every((name) => a.values[name] === b.values[name])
  );
}

function isEntry(value: unknown): value is CacheEntry {
  if (typeof value !== 'object' || value === null) return false;
  const e = value as Record<string, unknown>;
  const values = e['values'];
  return (
    typeof values === 'object' &&
    values !== null &&
    Object.values(values).every((v) => typeof v === 'string') &&
    typeof e['runId'] === 'string' &&
    e['runId'] !== '' &&
    typeof e['capturedAt'] === 'number' &&
    typeof e['stepHash'] === 'string' &&
    // An entry of an earlier version that the user pinned has no run to name.
    e['pinned'] !== true
  );
}

const STORAGE_VERSION = 1;

/** Reads the stored text of a workflow. A text that is not valid gives no entry. */
export function parseEntries(raw: string | null): CacheEntries {
  if (!raw) return {};
  try {
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== 'object' || parsed === null) return {};
    const body = parsed as { v?: unknown; entries?: unknown };
    if (body.v !== STORAGE_VERSION) return {};
    if (typeof body.entries !== 'object' || body.entries === null) return {};
    return Object.fromEntries(
      Object.entries(body.entries).filter(([, entry]) => isEntry(entry))
    );
  } catch {
    return {};
  }
}

/** The part of Storage that the cache uses. */
export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
  readonly length?: number;
  key?(index: number): string | null;
}

export interface OutputCache {
  /** The entries of a workflow. The same object until an entry changes. */
  entries(scope: CacheScope): CacheEntries;
  /** True when the entry is stored (see `putEntry`). */
  put(scope: CacheScope, step: string, entry: CacheEntry): boolean;
  remove(scope: CacheScope, step: string): void;
  /** Calls the listener after each change. Gives the function that stops it. */
  subscribe(listener: () => void): () => void;
  /** Forgets every entry, in memory and in the storage. */
  clear(): void;
}

const KEY_PREFIX = 'dagu.output-cache.v1:';

/**
 * The cache with a storage behind it. The entries stay in memory, so the
 * cache works when the storage is absent, blocked or full; each failure of
 * the storage is silent.
 */
export function createOutputCache(
  storage: () => StorageLike | null
): OutputCache {
  const memory = new Map<string, CacheEntries>();
  const listeners = new Set<() => void>();
  // Each part is encoded: a name can hold the separator.
  const storageKey = (scope: CacheScope) =>
    `${KEY_PREFIX}${encodeURIComponent(scope.remoteNode)}:${encodeURIComponent(scope.fileName)}`;
  const memoryKey = (scope: CacheScope) =>
    `${scope.remoteNode}\u0000${scope.fileName}`;
  function tryStorage<T>(use: (s: StorageLike) => T): T | undefined {
    try {
      const s = storage();
      return s ? use(s) : undefined;
    } catch {
      return undefined;
    }
  }

  const entries = (scope: CacheScope): CacheEntries => {
    const key = memoryKey(scope);
    const held = memory.get(key);
    if (held) return held;
    const loaded = parseEntries(
      tryStorage((s) => s.getItem(storageKey(scope))) ?? null
    );
    memory.set(key, loaded);
    return loaded;
  };

  const commit = (scope: CacheScope, next: CacheEntries) => {
    memory.set(memoryKey(scope), next);
    tryStorage((s) => {
      if (Object.keys(next).length === 0) s.removeItem(storageKey(scope));
      else
        s.setItem(
          storageKey(scope),
          JSON.stringify({ v: STORAGE_VERSION, entries: next })
        );
    });
    listeners.forEach((listener) => listener());
  };

  return {
    entries,
    put(scope, step, entry) {
      const result = putEntry(entries(scope), step, entry);
      if (result.stored) commit(scope, result.entries);
      return result.stored;
    },
    remove(scope, step) {
      const current = entries(scope);
      const next = removeEntry(current, step);
      if (next !== current) commit(scope, next);
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    clear() {
      memory.clear();
      tryStorage((s) => {
        const keys: string[] = [];
        for (let i = 0; i < (s.length ?? 0); i++) {
          const key = s.key?.(i);
          if (key?.startsWith(KEY_PREFIX)) keys.push(key);
        }
        keys.forEach((key) => s.removeItem(key));
      });
      listeners.forEach((listener) => listener());
    },
  };
}

/** The cache of the page, on `localStorage`. */
export const outputCache: OutputCache = createOutputCache(() =>
  typeof window === 'undefined' ? null : window.localStorage
);
