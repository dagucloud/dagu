// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { ThemeSelector } from '../ThemeSelector';

describe('ThemeSelector', () => {
  it('shows the current choice once, with a single icon', () => {
    localStorage.setItem('user_preferences', JSON.stringify({ theme: 'dark' }));

    render(
      <UserPreferencesProvider>
        <ThemeSelector />
      </UserPreferencesProvider>
    );

    const trigger = screen.getByRole('combobox', { name: 'Theme' });
    expect(trigger).toHaveTextContent(/^Dark$/);
    expect(trigger.querySelectorAll('svg.lucide-moon')).toHaveLength(1);
  });
});
