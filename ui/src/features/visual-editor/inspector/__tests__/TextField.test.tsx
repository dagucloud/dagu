// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { issuesSample } from '../../testing/fixtures';
import { TextField } from '../TextField';

function setup(props: Partial<Parameters<typeof TextField>[0]> = {}) {
  const onCommit = vi.fn();
  const view = render(
    <TextField
      label="Name"
      value="fetch"
      readOnly={false}
      onCommit={onCommit}
      {...props}
    />
  );
  return { ...view, onCommit, input: screen.getByLabelText('Name') };
}

describe('TextField', () => {
  it('sends the draft on blur when it changed', () => {
    const { onCommit, input } = setup();
    fireEvent.change(input, { target: { value: 'load' } });
    expect(onCommit).not.toHaveBeenCalled();
    fireEvent.blur(input);
    expect(onCommit).toHaveBeenCalledWith('load');
  });

  it('sends the draft on Enter', () => {
    const { onCommit, input } = setup();
    fireEvent.change(input, { target: { value: 'load' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onCommit).toHaveBeenCalledWith('load');
  });

  // Enter and Escape of an input method confirm or cancel its text.
  it('keeps the draft while an input method composes', () => {
    const { onCommit, input } = setup();
    fireEvent.change(input, { target: { value: 'load' } });
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true });
    fireEvent.keyDown(input, { key: 'Escape', isComposing: true });
    expect(onCommit).not.toHaveBeenCalled();
    expect(input).toHaveValue('load');
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onCommit).toHaveBeenCalledWith('load');
  });

  it('keeps the draft of a text area while an input method composes', () => {
    const { input } = setup({ multiline: true });
    fireEvent.change(input, { target: { value: 'load' } });
    fireEvent.keyDown(input, { key: 'Escape', isComposing: true });
    expect(input).toHaveValue('load');
  });

  it('sends nothing when the text did not change', () => {
    const { onCommit, input } = setup();
    fireEvent.blur(input);
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onCommit).not.toHaveBeenCalled();
  });

  it('restores the value on Escape', () => {
    const { onCommit, input } = setup();
    fireEvent.change(input, { target: { value: 'load' } });
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(input).toHaveValue('fetch');
    fireEvent.blur(input);
    expect(onCommit).not.toHaveBeenCalled();
  });

  it('resets the draft when the value changes from outside', () => {
    const { rerender, input, onCommit } = setup();
    fireEvent.change(input, { target: { value: 'typed' } });
    rerender(
      <TextField
        label="Name"
        value="renamed"
        readOnly={false}
        onCommit={onCommit}
      />
    );
    expect(input).toHaveValue('renamed');
  });

  it('is disabled and sends nothing when read-only', () => {
    const { input, onCommit } = setup({ readOnly: true });
    expect(input).toBeDisabled();
    fireEvent.blur(input);
    expect(onCommit).not.toHaveBeenCalled();
  });

  it('renders a text area and the field issues', () => {
    setup({ multiline: true, issues: [issuesSample[0]!] });
    expect(screen.getByLabelText('Name').tagName).toBe('TEXTAREA');
    expect(screen.getByLabelText('Name')).toHaveAttribute(
      'aria-invalid',
      'true'
    );
    expect(screen.getByRole('alert')).toHaveTextContent('unknown dependency');
  });
});
