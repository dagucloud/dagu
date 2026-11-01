// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { SaveDiffDialog } from '../SaveDiffDialog';

vi.mock('react-diff-viewer-continued', () => ({
  DiffMethod: { LINES: 'lines' },
  default: (props: {
    oldValue: string;
    newValue: string;
    splitView?: boolean;
    useDarkTheme?: boolean;
  }) => (
    <div
      data-testid="diff"
      data-split={String(props.splitView)}
      data-dark={String(props.useDarkTheme)}
      data-old={props.oldValue}
      data-new={props.newValue}
    />
  ),
}));

vi.mock('@/contexts/UserPreference', () => ({
  useResolvedTheme: () => 'light',
}));

const original = 'name: etl\nsteps:\n  - name: a\n    command: echo a\n';
const edited =
  'name: etl\nsteps:\n  - name: a\n    command: echo b\n  - name: c\n    command: echo c\n';

function setup(over: Partial<Parameters<typeof SaveDiffDialog>[0]> = {}) {
  const props = {
    open: true,
    original,
    edited,
    saving: false,
    onConfirm: vi.fn(),
    onCancel: vi.fn(),
    ...over,
  };
  render(<SaveDiffDialog {...props} />);
  return props;
}

describe('SaveDiffDialog scroll', () => {
  // The change is on line 40 of 50: the view starts a few lines above it.
  it('scrolls to the first changed line of a long file', () => {
    const lines = Array.from({ length: 50 }, (_, i) => `line ${i}`);
    const changed = lines.map((l, i) => (i === 40 ? 'changed' : l));
    const height = vi
      .spyOn(HTMLElement.prototype, 'scrollHeight', 'get')
      .mockReturnValue(5100);
    const top = vi.fn();
    const set = vi
      .spyOn(HTMLElement.prototype, 'scrollTop', 'set')
      .mockImplementation(top);
    try {
      setup({ original: lines.join('\n'), edited: changed.join('\n') });
      // 5100 / 51 = 100 px a line; (40 - 3) lines down.
      expect(top).toHaveBeenCalledWith(3700);
    } finally {
      height.mockRestore();
      set.mockRestore();
    }
  });
});

describe('SaveDiffDialog', () => {
  it('renders nothing visible when closed', () => {
    setup({ open: false });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.queryByTestId('diff')).toBeNull();
  });

  it('shows the diff and the line counts', () => {
    setup();
    expect(screen.getByText('Review changes')).toBeInTheDocument();
    const diff = screen.getByTestId('diff');
    expect(diff).toHaveAttribute('data-old', original);
    expect(diff).toHaveAttribute('data-new', edited);
    expect(screen.getByText('3 added, 1 removed')).toBeInTheDocument();
  });

  it('shows "No changes" for equal texts', () => {
    setup({ edited: original });
    expect(screen.getByText('No changes')).toBeInTheDocument();
    expect(screen.queryByTestId('diff')).toBeNull();
  });

  it('calls onConfirm', () => {
    const p = setup();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(p.onConfirm).toHaveBeenCalledTimes(1);
  });

  it('calls onCancel from the Cancel button', () => {
    const p = setup();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(p.onCancel).toHaveBeenCalledTimes(1);
  });

  it('switches the viewer between split and unified', () => {
    setup();
    expect(screen.getByTestId('diff')).toHaveAttribute('data-split', 'true');
    fireEvent.click(screen.getByRole('button', { name: 'Unified' }));
    expect(screen.getByTestId('diff')).toHaveAttribute('data-split', 'false');
    fireEvent.click(screen.getByRole('button', { name: 'Split' }));
    expect(screen.getByTestId('diff')).toHaveAttribute('data-split', 'true');
  });

  it('disables the actions while saving', () => {
    const p = setup({ saving: true });
    const save = screen.getByRole('button', { name: 'Save' });
    expect(save).toBeDisabled();
    expect(save.querySelector('svg.animate-spin')).not.toBeNull();
    expect(save).toHaveTextContent('Save');
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
    fireEvent.click(save);
    expect(p.onConfirm).not.toHaveBeenCalled();
  });

  it('calls onCancel on Escape', () => {
    const p = setup();
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(p.onCancel).toHaveBeenCalledTimes(1);
  });

  it('ignores Escape while saving', () => {
    const p = setup({ saving: true });
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(p.onCancel).not.toHaveBeenCalled();
  });
});
