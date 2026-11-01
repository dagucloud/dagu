// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { MonacoField } from '../MonacoField';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('./monacoMock')).monacoModule
);
vi.mock('../monacoSetup', () => ({ monacoTheme: () => 'light' }));

// The editor chunk loads on the first render of a field, and the module
// keeps it: this file has one test, so that the first render is the one
// that waits.
it('holds the place of the editor until Monaco loads, then gives it the focus', async () => {
  render(
    <MonacoField
      value="echo a"
      language="shell"
      readOnly={false}
      lines={4}
      ariaLabel="Script"
    />
  );
  const frame = document.querySelector<HTMLElement>('[data-monaco-pending]');
  expect(frame).not.toBeNull();
  expect(frame).toHaveAttribute('aria-label', 'Script');
  expect((frame?.firstElementChild as HTMLElement).style.height).toBe('84px');

  // A host focuses the field before the editor exists.
  frame?.focus();
  const editor = await screen.findByTestId('monaco');
  expect(document.querySelector('[data-monaco-pending]')).toBeNull();
  expect(document.activeElement).toBe(editor);
});
