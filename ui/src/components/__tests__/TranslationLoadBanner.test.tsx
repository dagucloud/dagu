// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

// The setup file loads every catalog. Fresh modules start without them, as a
// tab does on its first load.
async function importFresh() {
  vi.resetModules();
  const [preferences, provider, catalogs, banner] = await Promise.all([
    import('@/contexts/UserPreference'),
    import('@/i18n/I18nProvider'),
    import('@/i18n/translateStatic'),
    import('../TranslationLoadBanner'),
  ]);
  return {
    UserPreferencesProvider: preferences.UserPreferencesProvider,
    I18nProvider: provider.I18nProvider,
    useI18n: provider.useI18n,
    loadStaticMessages: catalogs.loadStaticMessages,
    TranslationLoadBanner: banner.TranslationLoadBanner,
  };
}

// jsdom does not reload, so the reload is read from a stand-in location.
const realLocation = window.location;

afterEach(() => {
  vi.doUnmock('@/i18n/locales/de');
  vi.restoreAllMocks();
  localStorage.clear();
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: realLocation,
  });
});

describe('TranslationLoadBanner', () => {
  // A tab left open across a server upgrade asks for a chunk that no longer
  // exists. The page stays, and a reload brings the new version.
  it('offers a reload when a new language fails to load', async () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));
    vi.doMock('@/i18n/locales/de', () => {
      throw new Error('chunk not found');
    });
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const reload = vi.fn();
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { reload },
    });
    const {
      UserPreferencesProvider,
      I18nProvider,
      TranslationLoadBanner,
      ...i18n
    } = await importFresh();
    function SwitchToGerman() {
      const { setLocale } = i18n.useI18n();
      return <button onClick={() => setLocale('de')}>Switch</button>;
    }
    await act(() => i18n.loadStaticMessages('ja'));

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <TranslationLoadBanner />
          <SwitchToGerman />
        </I18nProvider>
      </UserPreferencesProvider>
    );
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Switch' }));
    });

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Die Übersetzungen konnten nicht geladen werden.'
    );
    expect(screen.getByRole('button', { name: 'Switch' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Neu laden' }));
    expect(reload).toHaveBeenCalledOnce();
  });
});
