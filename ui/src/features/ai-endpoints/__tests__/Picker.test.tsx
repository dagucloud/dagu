// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Picker, type PickerItem } from '../Picker';
import { optionNames, pickOption } from './pickerHelpers';

const MODELS: PickerItem[] = [
  { value: '', label: 'None' },
  { value: 'claude-opus-5-5', label: 'Opus 5.5', detail: 'claude-opus-5-5' },
  {
    value: 'claude-haiku-5',
    label: 'claude-haiku-5',
    detail: 'claude-haiku-5',
  },
];

function renderPicker(
  items: readonly PickerItem[] = MODELS,
  value = 'claude-opus-5-5',
  disabled = false
) {
  const onSelect = vi.fn();
  render(
    <Picker
      label="Model"
      items={items}
      value={value}
      onSelect={onSelect}
      disabled={disabled}
      emptyText="Nothing matches."
    >
      {value}
    </Picker>
  );
  return { onSelect, trigger: screen.getByRole('combobox', { name: 'Model' }) };
}

describe('Picker', () => {
  it('lists the items, checks the current one, and shows a distinct detail', () => {
    const { trigger } = renderPicker();
    expect(optionNames(trigger)).toEqual([
      'None',
      'Opus 5.5claude-opus-5-5',
      'claude-haiku-5',
    ]);
    fireEvent.click(trigger);
    const selected = screen.getByRole('option', { selected: true });
    expect(selected).toHaveTextContent('Opus 5.5');
  });

  it('reports a new item and gives the focus back to the trigger', () => {
    const { onSelect, trigger } = renderPicker();
    pickOption(trigger, 'None');
    expect(onSelect).toHaveBeenCalledWith('');
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it('does not report the current item again', () => {
    const { onSelect, trigger } = renderPicker();
    pickOption(trigger, /^Opus 5\.5/);
    expect(onSelect).not.toHaveBeenCalled();
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  });

  it('opens and picks with the keys of a listbox', () => {
    const { onSelect, trigger } = renderPicker();
    fireEvent.keyDown(trigger, { key: 'ArrowDown' });
    const list = screen.getByRole('listbox');
    expect(list).toHaveFocus();
    // The current item is active first: one step down is the next item.
    fireEvent.keyDown(list, { key: 'ArrowDown' });
    fireEvent.keyDown(list, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledWith('claude-haiku-5');
  });

  it('names the list after the trigger', () => {
    const { trigger } = renderPicker();
    fireEvent.click(trigger);
    expect(screen.getByRole('listbox', { name: 'Model' })).toHaveFocus();
  });

  it('names the list after the label element of the trigger', () => {
    render(
      <>
        <label htmlFor="effort">Effort</label>
        <Picker id="effort" items={MODELS} value="" onSelect={vi.fn()}>
          None
        </Picker>
      </>
    );
    fireEvent.click(screen.getByRole('combobox', { name: 'Effort' }));
    expect(screen.getByRole('listbox', { name: 'Effort' })).toBeInTheDocument();
  });

  it('moves to the item that starts with the typed letters', () => {
    const { onSelect, trigger } = renderPicker();
    fireEvent.click(trigger);
    const list = screen.getByRole('listbox');
    fireEvent.keyDown(list, { key: 'n' });
    fireEvent.keyDown(list, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledWith('');
  });

  it('shuts on Escape and on a press outside without a choice', () => {
    const { onSelect, trigger } = renderPicker();
    fireEvent.click(trigger);
    fireEvent.keyDown(screen.getByRole('listbox'), { key: 'Escape' });
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();

    fireEvent.click(trigger);
    fireEvent.pointerDown(document.body);
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('searches a long list by label, detail and value', () => {
    const many = Array.from({ length: 9 }, (_, index) => ({
      value: `model-${index}`,
      label: `Model ${index}`,
      detail: index === 7 ? 'the fast one' : undefined,
    }));
    const { onSelect, trigger } = renderPicker(many, 'model-0');
    fireEvent.click(trigger);
    const search = screen.getByRole('textbox');
    expect(search).toHaveFocus();

    fireEvent.change(search, { target: { value: 'FAST' } });
    expect(screen.getAllByRole('option')).toHaveLength(1);
    fireEvent.keyDown(search, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledWith('model-7');

    fireEvent.click(trigger);
    fireEvent.change(screen.getByRole('textbox'), {
      target: { value: 'nothing like it' },
    });
    expect(screen.queryAllByRole('option')).toHaveLength(0);
    expect(screen.getByRole('listbox')).toHaveTextContent('Nothing matches.');
  });

  it('keeps a short list without a search box', () => {
    const { trigger } = renderPicker();
    fireEvent.click(trigger);
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  it('keeps the list in a window that has no room for a row on either side', () => {
    const innerHeight = window.innerHeight;
    Object.defineProperty(window, 'innerHeight', {
      value: 60,
      configurable: true,
    });
    try {
      const { trigger } = renderPicker();
      vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue(
        DOMRect.fromRect({ x: 0, y: 16, width: 200, height: 28 })
      );
      fireEvent.click(trigger);
      const popover = screen.getByTestId('ai-picker-popover');
      const top = parseFloat(popover.style.top);
      const maxHeight = parseFloat(popover.style.maxHeight);
      expect(top).toBeGreaterThanOrEqual(0);
      expect(top + maxHeight).toBeLessThanOrEqual(60);
    } finally {
      Object.defineProperty(window, 'innerHeight', {
        value: innerHeight,
        configurable: true,
      });
    }
  });

  it('does not open while disabled', () => {
    const { trigger } = renderPicker(MODELS, '', true);
    fireEvent.click(trigger);
    fireEvent.keyDown(trigger, { key: 'ArrowDown' });
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  });
});
