// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import ExternalChangeDialog from '../ExternalChangeDialog';

describe('ExternalChangeDialog', () => {
  // Radix warns on the console when a dialog has no description.
  it('describes the dialog with its first paragraph and logs no warning', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    const error = vi
      .spyOn(console, 'error')
      .mockImplementation(() => undefined);
    render(
      <ExternalChangeDialog
        visible
        onDiscard={() => undefined}
        onIgnore={() => undefined}
      />
    );

    const dialog = screen.getByRole('dialog');
    const description = document.getElementById(
      dialog.getAttribute('aria-describedby') ?? ''
    );
    expect(description).toHaveTextContent('modified externally');
    expect(warn).not.toHaveBeenCalled();
    expect(error).not.toHaveBeenCalled();
    warn.mockRestore();
    error.mockRestore();
  });
});
