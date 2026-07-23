// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { ControllerDeleteDialog } from '../components/ControllerDeleteDialog';
import { ControllerPromptDialog } from '../components/ControllerPromptDialog';

describe('Controller mutation dialogs', () => {
  it('cannot dismiss a pending delete', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(
      <ControllerDeleteDialog
        target={{ id: 'ctrl_aaaaaaaaaaaaaaaa', name: 'Router' }}
        pending
        onClose={onClose}
        onDelete={vi.fn()}
      />
    );

    await user.keyboard('{Escape}');

    expect(onClose).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: 'Close' })).toBeNull();
  });

  it('cannot dismiss a pending prompt', async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    render(
      <ControllerPromptDialog
        open
        title="Start Controller"
        description="Start it."
        submitLabel="Start"
        pending
        onOpenChange={onOpenChange}
        onSubmit={vi.fn()}
      />
    );

    await user.keyboard('{Escape}');

    expect(onOpenChange).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: 'Close' })).toBeNull();
  });
});
