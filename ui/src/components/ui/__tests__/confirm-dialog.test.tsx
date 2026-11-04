// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { ConfirmDialog, type ConfirmDialogProps } from '../confirm-dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../dropdown-menu';

function Host(
  props: Partial<
    Omit<ConfirmDialogProps, 'visible' | 'dismissModal' | 'children'>
  >
) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Opener
      </button>
      <ConfirmDialog
        title="Sure?"
        buttonText="Do it"
        onSubmit={() => setOpen(false)}
        {...props}
        visible={open}
        dismissModal={() => setOpen(false)}
      >
        Body
      </ConfirmDialog>
    </>
  );
}

describe('ConfirmDialog', () => {
  it('focuses the confirm button by default', async () => {
    const user = userEvent.setup();
    render(<Host />);
    await user.click(screen.getByRole('button', { name: 'Opener' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Do it' })).toHaveFocus()
    );
  });

  it('focuses Cancel with initialFocus="cancel"', async () => {
    const user = userEvent.setup();
    render(<Host initialFocus="cancel" />);
    await user.click(screen.getByRole('button', { name: 'Opener' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
    );
  });

  it('focuses Cancel while the confirm button is disabled', async () => {
    const user = userEvent.setup();
    render(<Host submitDisabled />);
    await user.click(screen.getByRole('button', { name: 'Opener' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
    );
  });

  it('gives the focus back to the opener with restoreFocus', async () => {
    const user = userEvent.setup();
    render(<Host initialFocus="cancel" restoreFocus />);
    const opener = screen.getByRole('button', { name: 'Opener' });
    await user.click(opener);
    await screen.findByRole('dialog');
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(opener).toHaveFocus());
  });

  it('runs the secondary action between Cancel and the confirm button', async () => {
    const user = userEvent.setup();
    const onSecondary = vi.fn();
    render(<Host secondary={{ text: 'Maybe', onClick: onSecondary }} />);
    await user.click(screen.getByRole('button', { name: 'Opener' }));
    const dialog = await screen.findByRole('dialog');
    const names = Array.from(dialog.querySelectorAll('button'))
      .map((button) => button.textContent)
      .filter((text) => ['Cancel', 'Maybe', 'Do it'].includes(text ?? ''));
    expect(names).toEqual(['Cancel', 'Maybe', 'Do it']);
    await user.click(screen.getByRole('button', { name: 'Maybe' }));
    expect(onSecondary).toHaveBeenCalledTimes(1);
  });

  it('has a ghost Cancel and a py-4 body', async () => {
    const user = userEvent.setup();
    render(<Host />);
    await user.click(screen.getByRole('button', { name: 'Opener' }));
    await screen.findByRole('dialog');
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveClass(
      'bg-transparent'
    );
    expect(screen.getByText('Body')).toHaveClass('py-4');
  });

  // A menu item acts on the keydown of Enter: the dialog that it opens must
  // not take that same key as the confirmation.
  it('does not confirm with the Enter that chose it in a menu', async () => {
    const onSubmit = vi.fn();
    function MenuHost() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <DropdownMenu defaultOpen>
            <DropdownMenuTrigger>More</DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuItem onSelect={() => setOpen(true)}>
                Delete
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <ConfirmDialog
            title="Sure?"
            buttonText="Do it"
            onSubmit={onSubmit}
            visible={open}
            dismissModal={() => setOpen(false)}
          >
            Body
          </ConfirmDialog>
        </>
      );
    }
    render(<MenuHost />);

    fireEvent.keyDown(screen.getByRole('menuitem', { name: 'Delete' }), {
      key: 'Enter',
    });

    expect(await screen.findByRole('dialog')).toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
