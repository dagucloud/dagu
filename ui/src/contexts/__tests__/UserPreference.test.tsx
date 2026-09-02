// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider, useUserPreferences } from '../UserPreference';

const wrapper = ({ children }: { children: React.ReactNode }) => (
  <UserPreferencesProvider>{children}</UserPreferencesProvider>
);

describe('UserPreferencesProvider', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('copies legacy Wiki sorting preferences without deleting them', () => {
    localStorage.setItem(
      'user_preferences',
      JSON.stringify({ docSortField: 'mtime', docSortOrder: 'desc' })
    );

    const { result } = renderHook(() => useUserPreferences(), { wrapper });

    expect(result.current.preferences.wikiSortField).toBe('mtime');
    expect(result.current.preferences.wikiSortOrder).toBe('desc');
    expect(
      JSON.parse(localStorage.getItem('user_preferences') ?? '{}')
    ).toEqual(
      expect.objectContaining({
        wikiSortField: 'mtime',
        wikiSortOrder: 'desc',
        docSortField: 'mtime',
        docSortOrder: 'desc',
      })
    );
  });

  it('uses Wiki sorting defaults when no sorting preference was saved', () => {
    localStorage.setItem('user_preferences', JSON.stringify({ pageLimit: 25 }));

    const { result } = renderHook(() => useUserPreferences(), { wrapper });

    expect(result.current.preferences.wikiSortField).toBe('type');
    expect(result.current.preferences.wikiSortOrder).toBe('asc');
  });

  it('uses Wiki sorting defaults for invalid legacy values', () => {
    localStorage.setItem(
      'user_preferences',
      JSON.stringify({ docSortField: 'unknown', docSortOrder: 'sideways' })
    );

    const { result } = renderHook(() => useUserPreferences(), { wrapper });

    expect(result.current.preferences.wikiSortField).toBe('type');
    expect(result.current.preferences.wikiSortOrder).toBe('asc');
  });

  it('loads a saved Japanese locale', () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));

    const { result } = renderHook(() => useUserPreferences(), { wrapper });

    expect(result.current.preferences.locale).toBe('ja');
  });

  it('keeps migrated preferences when storage is read-only', () => {
    localStorage.setItem(
      'user_preferences',
      JSON.stringify({ docSortField: 'mtime', docSortOrder: 'desc' })
    );
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('read-only storage');
    });

    const { result } = renderHook(() => useUserPreferences(), { wrapper });

    expect(result.current.preferences.wikiSortField).toBe('mtime');
    expect(result.current.preferences.wikiSortOrder).toBe('desc');
  });
});
