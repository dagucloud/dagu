// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { DocumentSwitcher } from '../DocumentSwitcher';

const documents = [
  { value: 'parent', label: 'main (Parent)' },
  { value: 'child', label: 'child' },
];

describe('DocumentSwitcher', () => {
  // A frame narrower than 40rem hides the select and shows the overflow
  // button: the two stay in the DOM, and the container query picks one.
  it('has a select and an overflow entry for a narrow frame', () => {
    render(
      <DocumentSwitcher
        value="parent"
        onChange={vi.fn()}
        documents={documents}
      />
    );
    const select = screen.getByRole('combobox', { name: 'Document' });
    expect(select.closest('span')).toHaveClass('@max-[40rem]/ve:hidden');
    const overflow = screen.getByRole('button', { name: 'Switch document' });
    expect(overflow.closest('span')).toHaveClass(
      'hidden',
      '@max-[40rem]/ve:inline-flex'
    );
  });

  it('switches the document from the overflow entry', async () => {
    const onChange = vi.fn();
    render(
      <DocumentSwitcher
        value="parent"
        onChange={onChange}
        documents={documents}
      />
    );
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Switch document' }));
    expect(
      screen.getByRole('menuitemradio', { name: /main \(Parent\)/ })
    ).toBeChecked();
    await user.click(screen.getByRole('menuitemradio', { name: 'child' }));
    expect(onChange).toHaveBeenCalledWith('child');
  });
});
