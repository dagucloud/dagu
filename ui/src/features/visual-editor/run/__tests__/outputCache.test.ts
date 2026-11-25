// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  MAX_VALUE_BYTES,
  MAX_WORKFLOW_BYTES,
  createOutputCache,
  entryBytes,
  parseEntries,
  putEntry,
  removeEntry,
  sameEntry,
  type CacheEntries,
  type CacheEntry,
  type StorageLike,
} from '../outputCache';

function entry(p: Partial<CacheEntry> = {}): CacheEntry {
  return {
    values: { out: 'v' },
    runId: 'r1',
    capturedAt: 1000,
    stepHash: 'h',
    ...p,
  };
}

const scope = { remoteNode: 'local', fileName: 'wf' };

function memoryStorage(): StorageLike & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (key) => data.get(key) ?? null,
    setItem: (key, value) => void data.set(key, value),
    removeItem: (key) => void data.delete(key),
    get length() {
      return data.size;
    },
    key: (i) => [...data.keys()][i] ?? null,
  };
}

describe('putEntry', () => {
  it('stores an entry by step', () => {
    const { entries, stored } = putEntry({}, 'a', entry());
    expect(stored).toBe(true);
    expect(entries['a']).toEqual(entry());
  });

  it('does not cache a value of more than 64 KB', () => {
    const big = entry({ values: { out: 'x'.repeat(MAX_VALUE_BYTES + 1) } });
    const result = putEntry({}, 'a', big);
    expect(result.stored).toBe(false);
    expect(result.entries).toEqual({});
    const edge = entry({ values: { out: 'x'.repeat(MAX_VALUE_BYTES) } });
    expect(putEntry({}, 'a', edge).stored).toBe(true);
  });

  it('counts the bytes of a value, not its characters', () => {
    // Two bytes for each character: 40000 characters make 80000 bytes.
    const wide = entry({ values: { out: 'é'.repeat(40_000) } });
    expect(putEntry({}, 'a', wide).stored).toBe(false);
  });

  it('does not cache an entry with no value', () => {
    expect(putEntry({}, 'a', entry({ values: {} })).stored).toBe(false);
  });

  it('never stores an entry that names no run', () => {
    expect(putEntry({}, 'a', entry({ runId: '' })).stored).toBe(false);
  });

  it('replaces the entry of the same step', () => {
    const first = putEntry({}, 'a', entry());
    const next = putEntry(
      first.entries,
      'a',
      entry({ values: { out: 'new' } })
    );
    expect(next.stored).toBe(true);
    expect(next.entries['a']?.values).toEqual({ out: 'new' });
  });

  // Each entry takes about 60 KB: three fit under 200 KB, a fourth does not.
  it('drops the oldest entries first to stay under the cap', () => {
    const value = 'x'.repeat(60_000);
    const make = (at: number) =>
      entry({ values: { out: value }, capturedAt: at });
    let entries: CacheEntries = {};
    entries = putEntry(entries, 'old', make(1)).entries;
    entries = putEntry(entries, 'mid', make(2)).entries;
    entries = putEntry(entries, 'later', make(3)).entries;
    const result = putEntry(entries, 'new', make(4));
    expect(result.stored).toBe(true);
    expect(Object.keys(result.entries).sort()).toEqual(['later', 'mid', 'new']);
    const total = Object.entries(result.entries).reduce(
      (sum, [step, e]) => sum + entryBytes(step, e),
      0
    );
    expect(total).toBeLessThanOrEqual(MAX_WORKFLOW_BYTES);
  });
});

describe('removeEntry and sameEntry', () => {
  it('removes an entry and returns the same set when there is none', () => {
    const entries = { a: entry() };
    expect(removeEntry(entries, 'a')).toEqual({});
    expect(removeEntry(entries, 'b')).toBe(entries);
  });

  it('compares values, run and hash', () => {
    expect(sameEntry(entry(), entry({ capturedAt: 9 }))).toBe(true);
    expect(sameEntry(entry(), entry({ values: { out: 'w' } }))).toBe(false);
    expect(sameEntry(entry(), entry({ stepHash: 'g' }))).toBe(false);
    expect(sameEntry(undefined, entry())).toBe(false);
  });
});

describe('parseEntries', () => {
  it('reads what the cache wrote and drops what is not an entry', () => {
    const raw = JSON.stringify({
      v: 1,
      entries: { a: entry(), b: { values: 1 } },
    });
    expect(Object.keys(parseEntries(raw))).toEqual(['a']);
  });

  // An earlier version let the user type a value: it names no run, so it
  // is not read back.
  it('drops an entry that the user had pinned and an entry with no run', () => {
    const raw = JSON.stringify({
      v: 1,
      entries: {
        a: { ...entry(), pinned: true },
        b: { ...entry(), runId: '' },
        c: { ...entry(), pinned: false },
      },
    });
    expect(Object.keys(parseEntries(raw))).toEqual(['c']);
  });

  it('gives nothing for a text that is not valid or has another version', () => {
    expect(parseEntries(null)).toEqual({});
    expect(parseEntries('{not json')).toEqual({});
    expect(
      parseEntries(JSON.stringify({ v: 2, entries: { a: entry() } }))
    ).toEqual({});
  });
});

describe('createOutputCache', () => {
  it('keeps the entries of a workflow per remote node and file', () => {
    const cache = createOutputCache(() => memoryStorage());
    cache.put(scope, 'a', entry());
    expect(Object.keys(cache.entries(scope))).toEqual(['a']);
    expect(cache.entries({ ...scope, remoteNode: 'edge' })).toEqual({});
    expect(cache.entries({ ...scope, fileName: 'other' })).toEqual({});
  });

  it('writes to the storage and reads it back in a new cache', () => {
    const storage = memoryStorage();
    createOutputCache(() => storage).put(scope, 'a', entry());
    const again = createOutputCache(() => storage);
    expect(again.entries(scope)['a']).toEqual(entry());
  });

  it('keeps apart two scopes whose names join to the same text', () => {
    const storage = memoryStorage();
    createOutputCache(() => storage).put(
      { remoteNode: 'a:b', fileName: 'c' },
      'x',
      entry()
    );
    const again = createOutputCache(() => storage);
    expect(again.entries({ remoteNode: 'a', fileName: 'b:c' })).toEqual({});
    expect(
      Object.keys(again.entries({ remoteNode: 'a:b', fileName: 'c' }))
    ).toEqual(['x']);
  });

  it('works in memory when the storage is absent', () => {
    const cache = createOutputCache(() => null);
    expect(cache.put(scope, 'a', entry())).toBe(true);
    expect(cache.entries(scope)['a']).toEqual(entry());
  });

  it('works in memory when the storage throws', () => {
    const broken: StorageLike = {
      getItem: () => {
        throw new Error('blocked');
      },
      setItem: () => {
        throw new Error('full');
      },
      removeItem: () => {
        throw new Error('blocked');
      },
    };
    const cache = createOutputCache(() => broken);
    expect(cache.entries(scope)).toEqual({});
    expect(cache.put(scope, 'a', entry())).toBe(true);
    cache.remove(scope, 'a');
    expect(cache.entries(scope)).toEqual({});
    const throwing = createOutputCache(() => {
      throw new Error('no access');
    });
    expect(throwing.put(scope, 'a', entry())).toBe(true);
  });

  it('gives the same object until an entry changes, and tells the listeners', () => {
    const cache = createOutputCache(() => null);
    let calls = 0;
    const stop = cache.subscribe(() => (calls += 1));
    const before = cache.entries(scope);
    expect(cache.entries(scope)).toBe(before);
    cache.put(scope, 'a', entry());
    expect(cache.entries(scope)).not.toBe(before);
    expect(calls).toBe(1);
    cache.remove(scope, 'a');
    expect(calls).toBe(2);
    cache.remove(scope, 'a');
    expect(calls).toBe(2);
    stop();
    cache.put(scope, 'b', entry());
    expect(calls).toBe(2);
  });

  it('does not tell the listeners about a refused entry', () => {
    const cache = createOutputCache(() => null);
    let calls = 0;
    cache.subscribe(() => (calls += 1));
    expect(cache.put(scope, 'a', entry({ runId: '' }))).toBe(false);
    expect(calls).toBe(0);
  });

  it('clears the memory and the storage keys of the cache only', () => {
    const storage = memoryStorage();
    storage.setItem('other', 'keep');
    const cache = createOutputCache(() => storage);
    cache.put(scope, 'a', entry());
    cache.clear();
    expect(cache.entries(scope)).toEqual({});
    expect([...storage.data.keys()]).toEqual(['other']);
  });
});
