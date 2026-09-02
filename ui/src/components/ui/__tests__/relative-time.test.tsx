// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import RelativeTime from '../relative-time';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';

const NOW = new Date('2026-07-12T12:00:00Z');

function secondsAgo(seconds: number): string {
  return new Date(NOW.getTime() - seconds * 1000).toISOString();
}

describe('RelativeTime', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('renders the fallback for absent and invalid timestamps', () => {
    const { rerender } = render(
      <RelativeTime timestamp={null} fallback="Never" />
    );
    expect(screen.getByText('Never')).toBeInTheDocument();

    rerender(<RelativeTime timestamp="not-a-time" fallback="Unknown" />);
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });

  it.each([
    [5, '5s'],
    [60, '1m'],
    [3_600, '1h'],
    [86_400, '1d'],
  ])('formats the compact boundary at %i seconds', (seconds, expected) => {
    render(<RelativeTime timestamp={secondsAgo(seconds)} compact />);
    expect(screen.getByText(expected)).toBeInTheDocument();
  });

  it('updates the compact label when its interval ticks', () => {
    render(<RelativeTime timestamp={secondsAgo(4)} compact absolute={null} />);
    const value = screen.getByText('Now');
    expect(value).toHaveAttribute('title');

    act(() => vi.advanceTimersByTime(1_000));
    expect(screen.getByText('5s')).toBeInTheDocument();
  });

  it('formats relative time in Japanese', () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <RelativeTime timestamp={secondsAgo(180)} />
        </I18nProvider>
      </UserPreferencesProvider>
    );

    expect(screen.getByText('3分前')).toBeInTheDocument();
  });
});
