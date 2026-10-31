// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  StepRunConfirmDialog,
  type StepRunChoice,
} from '../StepRunConfirmDialog';

function choices(
  extra: Partial<Record<StepRunChoice['id'], Partial<StepRunChoice>>> = {}
) {
  const onSelect = {
    only: vi.fn(),
    changed: vi.fn(),
    run: vi.fn(),
  };
  const list: StepRunChoice[] = [
    { id: 'only', label: 'Step only', onSelect: onSelect.only, ...extra.only },
    {
      id: 'changed',
      label: 'Changed steps',
      onSelect: onSelect.changed,
      ...extra.changed,
    },
    { id: 'run', label: 'Run', onSelect: onSelect.run, ...extra.run },
  ];
  return { list, onSelect };
}

function renderDialog(
  list: StepRunChoice[],
  primary: StepRunChoice['id'] | null,
  onCancel = vi.fn()
) {
  const ui = (p: StepRunChoice['id'] | null) => (
    <StepRunConfirmDialog
      open
      title="Run step"
      choices={list}
      primary={p}
      onCancel={onCancel}
    >
      <p>body</p>
    </StepRunConfirmDialog>
  );
  const view = render(ui(primary));
  return {
    onCancel,
    rerender: (p: StepRunChoice['id'] | null) => view.rerender(ui(p)),
  };
}

const labels = () =>
  Array.from(
    screen.getByTestId('step-run-dialog').querySelectorAll('button')
  ).map((button) => button.textContent);

describe('StepRunConfirmDialog', () => {
  it('shows Cancel first and then the choices in their order', () => {
    renderDialog(choices().list, 'only');
    expect(labels()).toEqual(['Cancel', 'Step only', 'Changed steps', 'Run']);
  });

  it('gives the primary style and the focus to the primary choice only', () => {
    renderDialog(choices().list, 'changed');
    const changed = screen.getByRole('button', { name: 'Changed steps' });
    expect(changed).toHaveFocus();
    expect(changed).toHaveAttribute('data-confirm');
    expect(screen.getByRole('button', { name: 'Run' })).not.toHaveAttribute(
      'data-confirm'
    );
  });

  it('picks the choice that the user clicks', () => {
    const { list, onSelect } = choices();
    renderDialog(list, 'only');
    fireEvent.click(screen.getByRole('button', { name: 'Changed steps' }));
    expect(onSelect.changed).toHaveBeenCalledTimes(1);
    expect(onSelect.only).not.toHaveBeenCalled();
    expect(onSelect.run).not.toHaveBeenCalled();
  });

  it('cancels with the Cancel button and with Escape', () => {
    const { onCancel } = renderDialog(choices().list, 'only');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.keyDown(screen.getByTestId('step-run-dialog'), {
      key: 'Escape',
    });
    expect(onCancel).toHaveBeenCalledTimes(2);
  });

  it('puts the reason on a disabled choice and a spinner in its icon slot', () => {
    const { list, onSelect } = choices({
      only: { disabled: true, busy: true, title: 'Waits for run r9 to finish' },
    });
    renderDialog(list, 'run');
    const only = screen.getByRole('button', { name: 'Step only' });
    expect(only).toBeDisabled();
    expect(only.querySelector('.animate-spin')).not.toBeNull();
    expect(only.querySelector('svg:not(.animate-spin)')).toBeNull();
    expect(only.parentElement).toHaveAttribute(
      'title',
      'Waits for run r9 to finish'
    );
    fireEvent.click(only);
    expect(onSelect.only).not.toHaveBeenCalled();
  });

  it('has no focus on a button while no choice is primary, then moves it', () => {
    const { list } = choices();
    const { rerender } = renderDialog(list, null);
    expect(document.querySelectorAll('[data-confirm]')).toHaveLength(0);
    expect(screen.getByRole('button', { name: 'Run' })).not.toHaveFocus();

    rerender('run');

    expect(screen.getByRole('button', { name: 'Run' })).toHaveFocus();
  });

  it('moves the focus when the primary choice changes under it', () => {
    const { list } = choices();
    const { rerender } = renderDialog(list, 'run');
    expect(screen.getByRole('button', { name: 'Run' })).toHaveFocus();

    rerender('only');

    expect(screen.getByRole('button', { name: 'Step only' })).toHaveFocus();
  });

  it('keeps the focus of a user who moved it to another button', () => {
    const { list } = choices();
    const { rerender } = renderDialog(list, null);
    screen.getByRole('button', { name: 'Cancel' }).focus();

    rerender('run');

    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus();
  });

  it('sits on the dialog layer, above the node view', () => {
    renderDialog(choices().list, 'only');
    expect(screen.getByTestId('step-run-dialog')).toHaveClass('z-[80]');
    expect(screen.getByTestId('step-run-overlay')).toHaveClass('z-[80]');
  });
});
