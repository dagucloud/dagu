// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { describe, expect, it } from 'vitest';
import { Dialog, DialogContent, DialogTitle } from '../dialog';

// A dialog that a button opens by state, with no DialogTrigger.
function Host() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button onClick={() => setOpen(true)}>Opener</button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent aria-describedby={undefined}>
          <DialogTitle>Plain</DialogTitle>
          {/* A field that autofocuses takes the focus before the dialog does. */}
          <input aria-label="Name" autoFocus />
        </DialogContent>
      </Dialog>
    </>
  );
}

// A non-modal dialog beside a field of the page.
function NonModalHost() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button onClick={() => setOpen(true)}>Opener</button>
      <input aria-label="Outside" />
      <Dialog open={open} onOpenChange={setOpen} modal={false}>
        <DialogContent aria-describedby={undefined}>
          <DialogTitle>Beside</DialogTitle>
          <input aria-label="Name" autoFocus />
        </DialogContent>
      </Dialog>
    </>
  );
}

describe('dialog focus', () => {
  it('gives the focus back to the element that opened it', async () => {
    const user = userEvent.setup();
    render(<Host />);
    const opener = screen.getByRole('button', { name: 'Opener' });
    await user.click(opener);
    await screen.findByRole('dialog');
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(opener).toHaveFocus());
  });

  it('leaves the focus outside a non-modal dialog that a click outside closed', async () => {
    const user = userEvent.setup();
    render(<NonModalHost />);
    await user.click(screen.getByRole('button', { name: 'Opener' }));
    await screen.findByRole('dialog');
    const outside = screen.getByRole('textbox', { name: 'Outside' });
    await user.click(outside);
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(outside).toHaveFocus();
  });
});
