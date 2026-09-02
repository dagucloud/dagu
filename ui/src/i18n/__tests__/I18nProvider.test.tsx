// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProps } from '../I18nProps';
import { I18nProvider } from '../I18nProvider';
import { I18nText } from '../I18nText';

describe('I18nProvider', () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.lang = 'en';
  });

  it('sets the document language from the saved locale', async () => {
    localStorage.setItem(
      'user_preferences',
      JSON.stringify({ locale: 'zh-CN' })
    );

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <div />
        </I18nProvider>
      </UserPreferencesProvider>
    );

    await waitFor(() => expect(document.documentElement.lang).toBe('zh-CN'));
  });

  it('sets Japanese as the document language', async () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <div />
        </I18nProvider>
      </UserPreferencesProvider>
    );

    await waitFor(() => expect(document.documentElement.lang).toBe('ja'));
  });

  it('localizes static text and props', () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <I18nProps>
            <input aria-label="Search" placeholder="Search items..." />
          </I18nProps>
          <span>
            <I18nText text="Actions" />
          </span>
        </I18nProvider>
      </UserPreferencesProvider>
    );

    expect(screen.getByRole('textbox', { name: '検索' })).toHaveAttribute(
      'placeholder',
      '項目を検索...'
    );
    expect(screen.getByText('アクション')).toBeVisible();
  });
});
