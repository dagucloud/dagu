// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Status } from '@/api/v1/schema';
import { RunStatusDot } from '../RunStatusDot';

describe('RunStatusDot', () => {
  // The dot takes its colour from lib/statusPalette.ts. parity:dup-23
  it('gives an aborted run the aborted colour', () => {
    render(<RunStatusDot status={Status.Aborted} />);

    const dot = screen.getByRole('img', { name: 'Aborted' });
    expect(dot).toHaveClass('bg-[var(--status-aborted)]');
    expect(dot).toHaveAttribute('title', 'Aborted');
    expect(dot).toHaveAttribute('data-status', String(Status.Aborted));
  });

  it.each([
    [Status.Success, 'Succeeded', 'bg-[var(--status-success)]'],
    [Status.Failed, 'Failed', 'bg-[var(--status-error)]'],
    [Status.Queued, 'Queued', 'bg-[var(--status-neutral)]'],
  ])('colours status %s as its tone', (status, name, colour) => {
    render(<RunStatusDot status={status} />);

    expect(screen.getByRole('img', { name })).toHaveClass(colour);
  });

  it('pulses the dot of a waiting run when motion is allowed', () => {
    render(<RunStatusDot status={Status.Waiting} />);

    expect(screen.getByRole('img', { name: 'Waiting' })).toHaveClass(
      'bg-[var(--status-warning)]',
      'motion-safe:animate-pulse'
    );
  });
});
