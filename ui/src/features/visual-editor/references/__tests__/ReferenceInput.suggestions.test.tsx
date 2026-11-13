// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import type { ReferenceInputProps } from '../../contracts';
import { ReferenceInput } from '../ReferenceInput';

const MODELS = ['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5'];

function Host({
  initial = '',
  onValue,
  ...rest
}: {
  initial?: string;
  onValue?(value: string): void;
} & Partial<ReferenceInputProps>) {
  const [value, setValue] = useState(initial);
  return (
    <ReferenceInput
      value={value}
      onChange={(next) => {
        setValue(next);
        onValue?.(next);
      }}
      candidates={[]}
      aria-label="model"
      suggestions={MODELS}
      suggestionsLabel="Models of the endpoint"
      {...rest}
    />
  );
}

const field = () => screen.getByLabelText('model') as HTMLInputElement;

function focus() {
  act(() => field().focus());
}

describe('ReferenceInput suggestions', () => {
  it('shows no list until the field has the focus', () => {
    render(<Host />);
    expect(screen.queryByRole('listbox')).toBeNull();
    focus();
    const list = screen.getByRole('listbox', {
      name: 'Models of the endpoint',
    });
    expect(within(list).getAllByRole('option')).toHaveLength(3);
  });

  it('narrows the list as the user types', () => {
    render(<Host />);
    focus();
    fireEvent.change(field(), { target: { value: 'son' } });
    expect(
      within(screen.getByRole('listbox'))
        .getAllByRole('option')
        .map((o) => o.textContent)
    ).toEqual(['claude-sonnet-5-5']);
  });

  it('keeps free text: a value that the list lacks is kept, with no list and no error', () => {
    const onValue = vi.fn();
    render(<Host onValue={onValue} />);
    focus();
    fireEvent.change(field(), { target: { value: 'my-own-model' } });
    expect(field()).toHaveValue('my-own-model');
    expect(onValue).toHaveBeenLastCalledWith('my-own-model');
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(field()).not.toHaveAttribute('aria-invalid');
    // A reference is free text too, and the reference picker serves it.
    fireEvent.change(field(), { target: { value: '${params.MODEL}' } });
    expect(onValue).toHaveBeenLastCalledWith('${params.MODEL}');
    expect(
      screen.queryByRole('listbox', { name: 'Models of the endpoint' })
    ).toBeNull();
  });

  it('replaces the whole text with the name that the user clicks', () => {
    const onValue = vi.fn();
    render(<Host initial="cl" onValue={onValue} />);
    focus();
    fireEvent.click(screen.getByRole('option', { name: 'claude-haiku-5' }));
    expect(onValue).toHaveBeenLastCalledWith('claude-haiku-5');
    expect(field()).toHaveValue('claude-haiku-5');
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('moves with the arrow keys, picks with Enter and closes with Escape', () => {
    const onValue = vi.fn();
    render(<Host onValue={onValue} />);
    focus();
    fireEvent.keyDown(field(), { key: 'ArrowDown' });
    fireEvent.keyDown(field(), { key: 'ArrowDown' });
    expect(field()).toHaveAttribute(
      'aria-activedescendant',
      screen.getByRole('option', { name: 'claude-sonnet-5-5' }).id
    );
    fireEvent.keyDown(field(), { key: 'Enter' });
    expect(onValue).toHaveBeenLastCalledWith('claude-sonnet-5-5');

    fireEvent.change(field(), { target: { value: 'claude' } });
    expect(screen.getByRole('listbox')).toBeInTheDocument();
    fireEvent.keyDown(field(), { key: 'Escape' });
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('does nothing on Enter when no name is active, so the text stays as typed', () => {
    const onValue = vi.fn();
    render(<Host initial="claude" onValue={onValue} />);
    focus();
    fireEvent.keyDown(field(), { key: 'Enter' });
    expect(onValue).not.toHaveBeenCalled();
    expect(field()).toHaveValue('claude');
  });

  it('shows no list for a disabled field or without suggestions', () => {
    const { rerender } = render(<Host disabled />);
    focus();
    expect(screen.queryByRole('listbox')).toBeNull();
    rerender(<Host suggestions={[]} />);
    focus();
    expect(screen.queryByRole('listbox')).toBeNull();
    rerender(<Host suggestions={undefined} />);
    focus();
    expect(screen.queryByRole('listbox')).toBeNull();
  });
});
