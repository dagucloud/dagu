// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent } from '@testing-library/react';

// Dispatches an Escape that a nested layer, such as a Radix dialog or menu,
// already handled. Radix marks the Escape it dismisses with preventDefault.
export function dispatchHandledEscape(): void {
  const event = new KeyboardEvent('keydown', {
    key: 'Escape',
    bubbles: true,
    cancelable: true,
  });
  event.preventDefault();
  fireEvent(window, event);
}
