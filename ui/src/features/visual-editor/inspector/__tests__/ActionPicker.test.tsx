// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { catalogSample } from '../../testing/fixtures';
import { ActionPicker } from '../ActionPicker';

function setup(disabled = false) {
  const onPick = vi.fn();
  render(
    <ActionPicker
      catalog={catalogSample}
      current="http.request"
      disabled={disabled}
      onPick={onPick}
    />
  );
  return onPick;
}

const open = () =>
  fireEvent.click(screen.getByRole('button', { name: 'Change the action' }));

describe('ActionPicker', () => {
  it('shows the current entry', () => {
    setup();
    expect(
      screen.getByRole('button', { name: 'Change the action' })
    ).toHaveTextContent('HTTP request');
  });

  it('filters the entries with the search box', () => {
    setup();
    open();
    expect(screen.getAllByRole('option')).toHaveLength(5);
    fireEvent.change(screen.getByRole('combobox'), {
      target: { value: 'json' },
    });
    expect(screen.getAllByRole('option').map((o) => o.textContent)).toEqual([
      'jq filterjq.filter',
    ]);
  });

  it('picks an entry with a click', () => {
    const onPick = setup();
    open();
    fireEvent.click(screen.getByRole('option', { name: /Router/ }));
    expect(onPick).toHaveBeenCalledWith(catalogSample[3]);
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('moves with the arrow keys and picks with Enter', () => {
    const onPick = setup();
    open();
    const search = screen.getByRole('combobox');
    fireEvent.keyDown(search, { key: 'ArrowDown' });
    fireEvent.keyDown(search, { key: 'ArrowDown' });
    fireEvent.keyDown(search, { key: 'ArrowUp' });
    expect(screen.getAllByRole('option')[1]).toHaveAttribute(
      'aria-selected',
      'true'
    );
    fireEvent.keyDown(search, { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith(catalogSample[1]);
  });

  // The keys of an input method confirm or cancel its text.
  it('ignores the keys of an input method while it composes', () => {
    const onPick = setup();
    open();
    const search = screen.getByRole('combobox');
    fireEvent.keyDown(search, { key: 'ArrowDown', isComposing: true });
    fireEvent.keyDown(search, { key: 'Enter', isComposing: true });
    fireEvent.keyDown(search, { key: 'Escape', isComposing: true });
    expect(onPick).not.toHaveBeenCalled();
    expect(screen.getAllByRole('option')[0]).toHaveAttribute(
      'aria-selected',
      'true'
    );
    fireEvent.keyDown(search, { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith(catalogSample[0]);
  });

  it('closes with Escape without a pick', () => {
    const onPick = setup();
    open();
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Escape' });
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(onPick).not.toHaveBeenCalled();
  });

  it('tells when nothing matches', () => {
    const onPick = setup();
    open();
    fireEvent.change(screen.getByRole('combobox'), {
      target: { value: 'zzz' },
    });
    expect(screen.getByText('No matching actions')).toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });
    expect(onPick).not.toHaveBeenCalled();
  });

  it('is disabled when read-only', () => {
    setup(true);
    expect(
      screen.getByRole('button', { name: 'Change the action' })
    ).toBeDisabled();
  });
});
