// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { getNotificationRoot } from '../notification-root';
import { ToastProvider, useSimpleToast } from '../simple-toast';

function Trigger({ variant }: { variant?: 'success' | 'error' | 'info' }) {
  const { showToast } = useSimpleToast();
  return (
    <button type="button" onClick={() => showToast('Done', { variant })}>
      go
    </button>
  );
}

function show(variant?: 'success' | 'error' | 'info') {
  render(
    <ToastProvider>
      <Trigger variant={variant} />
    </ToastProvider>
  );
  act(() => screen.getByText('go').click());
}

// A successful action shows no notification in the middle of the screen.
describe('ToastProvider', () => {
  it('shows nothing for a success, which is the default variant', () => {
    show();
    expect(screen.queryByText('Done')).toBeNull();
  });

  it('shows nothing for an explicit success', () => {
    show('success');
    expect(screen.queryByText('Done')).toBeNull();
  });

  it('shows an error', () => {
    show('error');
    expect(screen.getByText('Done')).toBeInTheDocument();
  });

  // A note is a card in the bottom right corner, in the stack of the
  // notifications, and not a box in the middle of the screen.
  it('draws a note in the stack of the bottom right corner', () => {
    show('info');
    const toast = screen.getByTestId('toast');
    expect(toast.parentElement).toBe(getNotificationRoot());
    expect(getNotificationRoot()).toHaveClass('fixed', 'bottom-4', 'right-4');
    expect(toast).toHaveTextContent('Done');
  });

  it('closes a note with its button', () => {
    show('info');
    act(() => screen.getByRole('button', { name: 'Dismiss' }).click());
    expect(screen.queryByText('Done')).toBeNull();
  });

  it('shows an info note', () => {
    show('info');
    expect(screen.getByText('Done')).toBeInTheDocument();
  });
});
