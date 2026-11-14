// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { DocumentTabs } from '../DocumentTabs';

const documents = [
  { value: 'parent', label: 'main', parent: true },
  { value: 'child', label: 'child' },
  { value: 'other', label: 'other' },
];

function renderTabs(value = 'parent') {
  const onChange = vi.fn();
  render(
    <DocumentTabs value={value} onChange={onChange} documents={documents} />
  );
  return onChange;
}

describe('DocumentTabs', () => {
  it('marks the shown workflow and switches on a click', () => {
    const onChange = renderTabs();

    expect(screen.getByRole('tab', { name: 'main' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    fireEvent.click(screen.getByRole('tab', { name: 'child' }));

    expect(onChange).toHaveBeenCalledWith('child');
  });

  // Only the shown tab takes the Tab key; the arrows move between the tabs
  // and wrap around.
  it('moves between the tabs with the arrow keys', () => {
    const onChange = renderTabs('parent');
    const tablist = screen.getByRole('tablist', { name: 'Document' });

    expect(screen.getByRole('tab', { name: 'main' })).toHaveAttribute(
      'tabindex',
      '0'
    );
    fireEvent.keyDown(tablist, { key: 'ArrowLeft' });
    fireEvent.keyDown(tablist, { key: 'ArrowRight' });

    expect(onChange.mock.calls).toEqual([['other'], ['child']]);
  });
});
