// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { MonacoField } from '../MonacoField';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('./monacoMock')).monacoModule
);
vi.mock('../monacoSetup', () => ({ monacoTheme: () => 'light' }));

async function setup(readOnly = false) {
  const onChange = vi.fn();
  const onCommit = vi.fn();
  const view = render(
    <MonacoField
      value="echo a"
      language="shell"
      readOnly={readOnly}
      ariaLabel="Script"
      onChange={onChange}
      onCommit={onCommit}
    />
  );
  return {
    ...view,
    onChange,
    onCommit,
    editor: await screen.findByTestId('monaco'),
  };
}

describe('MonacoField', () => {
  it('reports each change and commits on blur', async () => {
    const { editor, onChange, onCommit } = await setup();
    fireEvent.change(editor, { target: { value: 'echo b' } });
    expect(onChange).toHaveBeenCalledWith('echo b');
    expect(onCommit).not.toHaveBeenCalled();
    fireEvent.blur(editor);
    expect(onCommit).toHaveBeenCalledWith('echo b');
  });

  it('does not commit an unchanged text', async () => {
    const { editor, onCommit } = await setup();
    fireEvent.blur(editor);
    expect(onCommit).not.toHaveBeenCalled();
  });

  it('resets the draft when the value changes from outside', async () => {
    const { editor, rerender, onCommit } = await setup();
    fireEvent.change(editor, { target: { value: 'typed' } });
    rerender(
      <MonacoField
        value="echo c"
        language="shell"
        readOnly={false}
        ariaLabel="Script"
        onCommit={onCommit}
      />
    );
    expect(editor).toHaveValue('echo c');
  });

  it('does not commit when read-only', async () => {
    const { editor, onCommit } = await setup(true);
    expect(editor).toHaveAttribute('readonly');
    fireEvent.change(editor, { target: { value: 'x' } });
    fireEvent.blur(editor);
    expect(onCommit).not.toHaveBeenCalled();
  });

  it('does not commit after it becomes read-only', async () => {
    const { editor, rerender, onCommit } = await setup();
    fireEvent.change(editor, { target: { value: 'typed' } });
    rerender(
      <MonacoField
        value="echo a"
        language="shell"
        readOnly
        ariaLabel="Script"
        onCommit={onCommit}
      />
    );
    fireEvent.blur(editor);
    expect(onCommit).not.toHaveBeenCalled();
  });
});
