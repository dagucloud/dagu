// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { FlowSection } from '../sections/Flow';
import { runStep } from './fixtures';
import { fieldOp, renderPart } from './flowHarness';

describe('FlowSection', () => {
  it('shows every flow setting of the step', () => {
    renderPart(FlowSection, runStep);
    expect(screen.getByText('Depends on')).toBeInTheDocument();
    expect(screen.getByLabelText('Timeout (seconds)')).toHaveValue('60');
    expect(
      screen.getByRole('switch', { name: 'Continue on failure' })
    ).toBeInTheDocument();
    expect(screen.getByText('Preconditions')).toBeInTheDocument();
    expect(
      screen.getByRole('group', { name: 'Environment' })
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Output name')).toHaveValue('artifact');
  });

  it('edits one environment value of a list of maps', () => {
    const { onEdit } = renderPart(FlowSection, runStep);
    const env = screen.getByRole('group', { name: 'Environment' });
    const value = env.querySelector(
      'input[aria-label="Value"]'
    ) as HTMLInputElement;
    expect(value).toHaveValue('-mod=mod');
    fireEvent.change(value, { target: { value: '-mod=vendor' } });
    fireEvent.blur(value);
    expect(onEdit).toHaveBeenCalledTimes(1);
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['env', 0, 'GOFLAGS'], '-mod=vendor')
    );
  });

  it('emits nothing when read-only', () => {
    const { onEdit, edit } = renderPart(FlowSection, runStep, {
      readOnly: true,
    });
    edit('Timeout (seconds)', '1');
    fireEvent.click(screen.getByRole('switch', { name: 'Mark as success' }));
    expect(onEdit).not.toHaveBeenCalled();
  });
});
