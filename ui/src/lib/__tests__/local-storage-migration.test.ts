// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { readMigratedLocalStorage } from '../local-storage-migration';

describe('readMigratedLocalStorage', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('returns the legacy value when storage is read-only', () => {
    localStorage.setItem('legacy-key', 'legacy-value');
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('read-only storage');
    });

    expect(readMigratedLocalStorage('current-key', 'legacy-key')).toBe(
      'legacy-value'
    );
  });
});
