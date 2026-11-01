// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  UserPreferencesProvider,
  useResolvedTheme,
  useUserPreferences,
} from '../UserPreference';

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

  it('reads the order of the log lines and drops a value that is not known', () => {
    localStorage.setItem(
      'user_preferences',
      JSON.stringify({ logOrder: 'newToOld' })
    );
    const saved = renderHook(() => useUserPreferences(), { wrapper });
    expect(saved.result.current.preferences.logOrder).toBe('newToOld');
    saved.unmount();

    localStorage.setItem(
      'user_preferences',
      JSON.stringify({ logOrder: 'sideways' })
    );
    const bad = renderHook(() => useUserPreferences(), { wrapper });
    expect(bad.result.current.preferences.logOrder).toBe('oldToNew');
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

  it('updates preferences when storage writes fail', () => {
    const { result } = renderHook(() => useUserPreferences(), { wrapper });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('read-only storage');
    });

    act(() => {
      result.current.updatePreference('pinnedViewOrder', {
        local: ['reports', 'workflows'],
      });
    });

    expect(result.current.preferences.pinnedViewOrder).toEqual({
      local: ['reports', 'workflows'],
    });
    expect(result.current.preferences.theme).toBe('system');
  });

  // The workflow page has one body; an old stored choice is dropped.
  it('drops the stored workflow page choice of an older version', () => {
    localStorage.setItem(
      'user_preferences',
      JSON.stringify({ workflowPage: 'classic', locale: 'ja' })
    );

    const { result } = renderHook(() => useUserPreferences(), { wrapper });

    expect(result.current.preferences).not.toHaveProperty('workflowPage');
    expect(result.current.preferences.locale).toBe('ja');
    expect(
      JSON.parse(localStorage.getItem('user_preferences') ?? '{}')
    ).not.toHaveProperty('workflowPage');
  });

  describe('useResolvedTheme', () => {
    let prefersDark = false;
    let notifyChange: (() => void) | undefined;

    beforeEach(() => {
      prefersDark = false;
      notifyChange = undefined;
      vi.stubGlobal(
        'matchMedia',
        vi.fn(() => ({
          get matches() {
            return prefersDark;
          },
          addEventListener: (_: string, listener: () => void) => {
            notifyChange = listener;
          },
          removeEventListener: vi.fn(),
        }))
      );
    });

    afterEach(() => {
      vi.unstubAllGlobals();
    });

    const useThemeState = () => ({
      resolved: useResolvedTheme(),
      ...useUserPreferences(),
    });

    it('follows the operating system by default', () => {
      const { result } = renderHook(useThemeState, { wrapper });
      expect(result.current.preferences.theme).toBe('system');
      expect(result.current.resolved).toBe('light');

      act(() => {
        prefersDark = true;
        notifyChange?.();
      });
      expect(result.current.resolved).toBe('dark');
    });

    it('uses an explicit choice over the operating system', () => {
      prefersDark = true;
      const { result } = renderHook(useThemeState, { wrapper });

      act(() => result.current.updatePreference('theme', 'light'));

      expect(result.current.resolved).toBe('light');
    });

    it('treats an unknown saved theme as system', () => {
      prefersDark = true;
      localStorage.setItem(
        'user_preferences',
        JSON.stringify({ theme: 'sepia' })
      );

      const { result } = renderHook(useThemeState, { wrapper });

      expect(result.current.preferences.theme).toBe('system');
      expect(result.current.resolved).toBe('dark');
    });
  });
});
