// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RefreshButton } from '@/components/ui/refresh-button';

describe('RefreshButton', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('shows the spinner briefly after a refresh', async () => {
    const onRefresh = vi.fn();
    render(<RefreshButton onRefresh={onRefresh} />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button'));
    });
    expect(onRefresh).toHaveBeenCalledOnce();
    expect(screen.getByRole('button')).toHaveAttribute('aria-busy', 'true');

    await act(async () => {
      vi.advanceTimersByTime(500);
    });
    expect(screen.getByRole('button')).not.toHaveAttribute('aria-busy');
  });

  // A refresh that ends just before the button goes away leaves no timer
  // that would update it afterwards.
  it('clears its feedback timer when it unmounts', async () => {
    const view = render(<RefreshButton onRefresh={vi.fn()} />);
    await act(async () => {
      fireEvent.click(screen.getByRole('button'));
    });
    expect(vi.getTimerCount()).toBe(1);

    view.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
