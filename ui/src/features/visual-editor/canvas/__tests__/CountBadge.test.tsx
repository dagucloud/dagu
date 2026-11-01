// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { CountBadge } from '../CountBadge';
import { LinkButton } from '../LinkButton';

const SHAPE = [
  'h-5',
  'min-w-5',
  'rounded-full',
  'px-1',
  'text-[11px]',
  'font-semibold',
  'leading-none',
  'shadow-sm',
];

// The owner asked for one badge: the Cron count, the errors and the warnings
// of a step all have the same shape, and the errors have the red of the trash.
describe('CountBadge', () => {
  it('has one shape for every tone', () => {
    const { getByTestId } = render(
      <>
        <CountBadge data-testid="cron" count={2} corner />
        <CountBadge data-testid="error" count={3} tone="error" />
        <CountBadge data-testid="warning" count={1} tone="warning" />
      </>
    );
    for (const id of ['cron', 'error', 'warning']) {
      expect(getByTestId(id)).toHaveClass(...SHAPE);
    }
    expect(getByTestId('cron')).toHaveTextContent('2');
    expect(getByTestId('error')).toHaveTextContent('3');
  });

  it('pins itself on the corner only when asked', () => {
    const { getByTestId } = render(
      <>
        <CountBadge data-testid="corner" count={1} corner />
        <CountBadge data-testid="flow" count={1} />
      </>
    );
    expect(getByTestId('corner')).toHaveClass('absolute', '-right-2', '-top-2');
    expect(getByTestId('flow')).not.toHaveClass('absolute');
  });

  it('uses the destructive token for an error, as the trash button does', () => {
    const { getByTestId, getByRole } = render(
      <>
        <CountBadge data-testid="error" count={1} tone="error" />
        <LinkButton kind="remove" aria-label="trash" />
      </>
    );
    expect(getByTestId('error')).toHaveClass(
      'bg-destructive',
      'text-destructive-foreground'
    );
    expect(getByRole('button', { name: 'trash' })).toHaveClass(
      'hover:bg-destructive',
      'hover:text-destructive-foreground'
    );
  });

  it('uses the warning token for a warning and primary for a count', () => {
    const { getByTestId } = render(
      <>
        <CountBadge data-testid="warning" count={1} tone="warning" />
        <CountBadge data-testid="count" count={1} />
      </>
    );
    expect(getByTestId('warning')).toHaveClass(
      'bg-warning',
      'text-warning-foreground'
    );
    expect(getByTestId('warning').className).not.toMatch(/amber-/);
    expect(getByTestId('count')).toHaveClass('bg-primary');
  });

  it('has an inline size for tabs and rows', () => {
    const { getByTestId } = render(
      <CountBadge data-testid="inline" count={4} size="inline" />
    );
    expect(getByTestId('inline')).toHaveClass('h-4', 'min-w-4', 'text-[10px]');
    expect(getByTestId('inline')).not.toHaveClass('h-5');
  });
});
