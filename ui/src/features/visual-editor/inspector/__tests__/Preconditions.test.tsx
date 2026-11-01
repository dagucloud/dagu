// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { conditionRows, Preconditions } from '../sections/flow/Preconditions';
import { runStep } from './fixtures';
import { fieldOp, renderPart } from './flowHarness';

const withConditions = {
  ...runStep,
  value: {
    preconditions: [
      { condition: '${ENV}', expected: 'prod' },
      { condition: 'test -f a' },
    ],
  },
};

describe('conditionRows', () => {
  it.each<[unknown, unknown]>([
    [undefined, []],
    [[{ condition: 'a' }], [{ condition: 'a' }]],
    ['test -f a', null],
    [['a'], null],
  ])('%j gives %j', (value, expected) => {
    expect(conditionRows(value)).toEqual(expected);
  });
});

describe('Preconditions', () => {
  it('edits one key of one condition', () => {
    const { onEdit, edit } = renderPart(Preconditions, withConditions);
    edit('Expected', 'staging');
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['preconditions', 0, 'expected'], 'staging')
    );
    edit('Condition', 'test -f b', 1);
    expect(onEdit).toHaveBeenLastCalledWith(
      fieldOp(['preconditions', 1, 'condition'], 'test -f b')
    );
  });

  it('removes a condition', () => {
    const { onEdit } = renderPart(Preconditions, withConditions);
    fireEvent.click(screen.getByRole('button', { name: 'Remove condition 2' }));
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['preconditions', 1], undefined)
    );
  });

  it('appends a condition', () => {
    const { onEdit } = renderPart(Preconditions, withConditions);
    fireEvent.change(screen.getByLabelText('New condition'), {
      target: { value: '${A}' },
    });
    fireEvent.change(screen.getByLabelText('New expected value'), {
      target: { value: '1' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add condition' }));
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['preconditions', 2], { condition: '${A}', expected: '1' })
    );
  });

  it('creates the list for the first condition', () => {
    const { onEdit } = renderPart(Preconditions, runStep);
    fireEvent.change(screen.getByLabelText('New condition'), {
      target: { value: 'true' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add condition' }));
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['preconditions'], [{ condition: 'true' }])
    );
  });

  it('shows a string precondition read-only', () => {
    renderPart(Preconditions, {
      ...runStep,
      value: { preconditions: 'test -f a' },
    });
    expect(screen.getByText('test -f a')).toBeInTheDocument();
    expect(screen.getByText('Edit this field in YAML')).toBeInTheDocument();
    expect(screen.queryByLabelText('New condition')).toBeNull();
  });

  // The server refuses `condition` next to `eval`.
  it('shows an eval precondition read-only', () => {
    renderPart(Preconditions, {
      ...runStep,
      value: {
        preconditions: [{ eval: '$(printf ready)', expected: 'ready' }],
      },
    });
    expect(screen.getByText('Edit this field in YAML')).toBeInTheDocument();
    expect(screen.queryByLabelText('Condition')).toBeNull();
  });

  it('emits nothing when read-only', () => {
    const { onEdit, edit } = renderPart(Preconditions, withConditions, {
      readOnly: true,
    });
    edit('Expected', 'x');
    expect(
      screen.getByRole('button', { name: 'Remove condition 1' })
    ).toBeDisabled();
    expect(onEdit).not.toHaveBeenCalled();
  });
});
