// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { I18nProvider } from '@/i18n/I18nProvider';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { RetryCount } from '../retry-count';

function renderCount(count: number) {
  return render(
    <UserPreferencesProvider>
      <I18nProvider>
        <RetryCount count={count} />
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

describe('RetryCount', () => {
  it('shows the count with a label and a tooltip that name the retries', () => {
    renderCount(3);
    const badge = screen.getByLabelText('Retried 3×');
    expect(badge).toHaveTextContent('3');
    expect(badge).toHaveAttribute('title', 'Retried 3×');
  });

  it('is a small badge with a small icon', () => {
    renderCount(1);
    const badge = screen.getByLabelText('Retried 1×');
    expect(badge).toHaveClass('h-4', 'tabular-nums');
    expect(badge.querySelector('svg')).toHaveClass('size-3');
  });
});
