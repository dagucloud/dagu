// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { StorageLike } from '../outputCache';
import {
  createSourceChoiceStore,
  parseChoice,
  serializeChoice,
} from '../runSourceChoice';

const scope = { remoteNode: 'local', fileName: 'wf' };

function memoryStorage(): StorageLike & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (key) => data.get(key) ?? null,
    setItem: (key, value) => void data.set(key, value),
    removeItem: (key) => void data.delete(key),
  };
}

describe('parseChoice and serializeChoice', () => {
  it('round-trips the three choices', () => {
    for (const choice of [
      { kind: 'auto' },
      { kind: 'cache' },
      { kind: 'run', runId: 'AbC1' },
    ] as const) {
      expect(parseChoice(serializeChoice(choice))).toEqual(choice);
    }
  });

  it('reads an unknown or empty text as the automatic choice', () => {
    expect(parseChoice(null)).toEqual({ kind: 'auto' });
    expect(parseChoice('')).toEqual({ kind: 'auto' });
    expect(parseChoice('run:')).toEqual({ kind: 'auto' });
    expect(parseChoice('pinned')).toEqual({ kind: 'auto' });
  });
});

describe('createSourceChoiceStore', () => {
  it('is the automatic choice until the user chooses', () => {
    const store = createSourceChoiceStore(() => memoryStorage());
    expect(store.get(scope)).toBe('auto');
  });

  it('keeps the choice for each workflow in the storage of the session', () => {
    const storage = memoryStorage();
    const store = createSourceChoiceStore(() => storage);
    store.set(scope, { kind: 'run', runId: 'RunA' });
    expect(store.get(scope)).toBe('run:RunA');
    expect(store.get({ ...scope, fileName: 'other' })).toBe('auto');
    expect(createSourceChoiceStore(() => storage).get(scope)).toBe('run:RunA');
    // The automatic choice leaves no key.
    store.set(scope, { kind: 'auto' });
    expect([...storage.data.keys()]).toEqual([]);
  });

  it('tells the listeners once for a change, not for the same choice', () => {
    const store = createSourceChoiceStore(() => null);
    let calls = 0;
    const stop = store.subscribe(() => (calls += 1));
    store.set(scope, { kind: 'cache' });
    store.set(scope, { kind: 'cache' });
    expect(calls).toBe(1);
    stop();
    store.set(scope, { kind: 'auto' });
    expect(calls).toBe(1);
  });

  it('works in memory when the storage is absent or throws', () => {
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
    for (const make of [() => null, () => broken]) {
      const store = createSourceChoiceStore(make);
      expect(store.get(scope)).toBe('auto');
      store.set(scope, { kind: 'cache' });
      expect(store.get(scope)).toBe('cache');
    }
  });
});
