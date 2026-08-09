// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { UserPreferencesProvider, useUserPreferences } from '../UserPreference';

const wrapper = ({ children }: { children: React.ReactNode }) => (
  <UserPreferencesProvider>{children}</UserPreferencesProvider>
);

describe('UserPreferencesProvider', () => {
  beforeEach(() => {
    localStorage.clear();
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
});
