// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ToolFilter } from '../ToolFilter';

describe('ToolFilter', () => {
  it('shows an empty box without a count', () => {
    render(<ToolFilter value="" onChange={vi.fn()} matches={4} total={4} />);
    expect(
      screen.getByRole('searchbox', { name: 'Filter tools by name' })
    ).toHaveValue('');
    expect(screen.queryByTestId('ve-tool-filter-count')).toBeNull();
    expect(
      screen.queryByRole('button', { name: 'Clear the filter' })
    ).toBeNull();
  });

  it('reports the typed text', () => {
    const onChange = vi.fn();
    render(<ToolFilter value="" onChange={onChange} matches={4} total={4} />);
    fireEvent.change(screen.getByRole('searchbox'), {
      target: { value: 'bui' },
    });
    expect(onChange).toHaveBeenCalledWith('bui');
  });

  it('shows the count of matches and clears with the button', () => {
    const onChange = vi.fn();
    render(
      <ToolFilter value="bui" onChange={onChange} matches={2} total={9} />
    );
    expect(screen.getByTestId('ve-tool-filter-count')).toHaveTextContent(
      '2 of 9 tools'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Clear the filter' }));
    expect(onChange).toHaveBeenCalledWith('');
  });

  // The placeholder is one short word, and the box keeps a floor that holds it.
  it('has a short placeholder and a floor in a compact bar', () => {
    render(
      <ToolFilter value="" onChange={vi.fn()} matches={0} total={3} compact />
    );
    const box = screen.getByRole('searchbox');
    expect(box).toHaveAttribute('placeholder', 'Filter');
    expect(box).toHaveClass('min-w-16', 'shrink');
    expect(box).not.toHaveClass('text-ellipsis');
  });

  it('clears with Escape and keeps keys from the canvas shortcuts', () => {
    const onChange = vi.fn();
    const onKey = vi.fn();
    render(
      <div onKeyDown={onKey}>
        <ToolFilter value="x" onChange={onChange} matches={0} total={3} />
      </div>
    );
    const box = screen.getByRole('searchbox');
    fireEvent.keyDown(box, { key: 'Delete' });
    fireEvent.keyDown(box, { key: 'Escape' });
    expect(onChange).toHaveBeenCalledWith('');
    expect(onKey).not.toHaveBeenCalled();
  });

  // Enter and Escape during an IME composition belong to the IME.
  it('ignores Enter and Escape while a composition is active', () => {
    const onChange = vi.fn();
    const onStep = vi.fn();
    render(
      <ToolFilter
        value="x"
        onChange={onChange}
        matches={1}
        total={3}
        onStep={onStep}
      />
    );
    const box = screen.getByRole('searchbox');
    fireEvent.keyDown(box, { key: 'Enter', isComposing: true });
    fireEvent.keyDown(box, { key: 'Escape', isComposing: true });
    expect(onStep).not.toHaveBeenCalled();
    expect(onChange).not.toHaveBeenCalled();
  });
});
