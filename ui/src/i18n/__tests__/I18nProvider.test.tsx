// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '../I18nProvider';

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
});
