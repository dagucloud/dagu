// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { I18nProvider } from '@/i18n/I18nProvider';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { RepeatBadge } from '../repeat-badge';
import type { RepeatPolicyView } from '../repeat-summary';

function renderBadge(props: {
  policy: RepeatPolicyView;
  count?: number;
  running?: boolean;
  compact?: boolean;
}) {
  return render(
    <UserPreferencesProvider>
      <I18nProvider>
        <RepeatBadge {...props} />
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

const policy: RepeatPolicyView = { mode: 'while', intervalSec: 10 };

describe('RepeatBadge', () => {
  it('shows the interval before the step has run, with the summary as its name', () => {
    renderBadge({ policy });
    const badge = screen.getByTestId('repeat-badge');
    expect(badge).toHaveTextContent('every 10s');
    expect(badge).toHaveAttribute(
      'title',
      'repeat while exit 0 · every 10s · no limit'
    );
    expect(badge).toHaveClass('h-4');
  });

  it('shows the run count and the iteration once the step has run', () => {
    renderBadge({ policy: { ...policy, limit: 5 }, count: 3 });
    const badge = screen.getByTestId('repeat-badge');
    expect(badge).toHaveTextContent('×3');
    expect(badge.getAttribute('title')).toBe(
      'repeat while exit 0 · every 10s · max 5\niteration 3 of 5'
    );
  });

  // The compact count rounds down; the title and the name give the whole count.
  it.each<[number, string, string]>([
    [999, '×999', '999'],
    [1234, '×1.2k', '1,234'],
    [34567, '×34k', '34,567'],
    [31_000_000, '×31M', '31,000,000'],
  ])(
    'writes %i as %s, with the whole count in its name',
    (count, text, exact) => {
      renderBadge({ policy, count });
      const badge = screen.getByTestId('repeat-badge');
      expect(badge).toHaveTextContent(text);
      expect(badge.getAttribute('title')).toContain(`iteration ${exact}`);
      expect(badge).toHaveAttribute('aria-label', badge.getAttribute('title'));
    }
  );

  it('writes the compact count in the compact form too', () => {
    renderBadge({ policy, count: 12_345, compact: true });
    expect(screen.getByTestId('repeat-badge')).toHaveTextContent('×12k');
  });

  it('shows the mode when the policy has no interval', () => {
    renderBadge({ policy: { mode: 'until' } });
    expect(screen.getByTestId('repeat-badge')).toHaveTextContent(/^until$/);
  });

  it('shows only the icon when compact and the step has not run', () => {
    renderBadge({ policy, compact: true });
    const badge = screen.getByTestId('repeat-badge');
    expect(badge).toHaveTextContent(/^$/);
    expect(badge.querySelector('svg')).not.toBeNull();
  });

  it('shows the count when compact and the step has run', () => {
    renderBadge({ policy, count: 4, compact: true });
    expect(screen.getByTestId('repeat-badge')).toHaveTextContent('×4');
  });

  it('names the running iteration, with no animation of its own', () => {
    renderBadge({ policy, count: 2, running: true });
    const badge = screen.getByTestId('repeat-badge');
    expect(badge).toHaveAttribute('data-running', 'true');
    expect(badge.className).not.toMatch(/animate-/);
    expect(badge.getAttribute('title')).toContain('iteration 3');
  });
});
