// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Outputs } from '../sections/flow/Outputs';
import { runStep } from './fixtures';
import { fieldOp, renderPart } from './flowHarness';

describe('Outputs', () => {
  it('renames an output', () => {
    const { onEdit, edit } = renderPart(Outputs, runStep);
    expect(screen.getByLabelText('Output name')).toHaveValue('artifact');
    edit('Output name', 'binary');
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['outputs', 0, 'name'], 'binary')
    );
  });

  it('does not rename to an empty name', () => {
    const { onEdit, edit } = renderPart(Outputs, runStep);
    edit('Output name', ' ');
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('removes an output', () => {
    const { onEdit } = renderPart(Outputs, {
      ...runStep,
      value: {
        ...runStep.value,
        outputs: [{ name: 'artifact' }, { name: 'log' }],
      },
    });
    fireEvent.click(
      screen.getByRole('button', { name: 'Remove output artifact' })
    );
    expect(onEdit).toHaveBeenCalledWith(fieldOp(['outputs', 0], undefined));
  });

  // The server refuses an empty list of outputs.
  it('removes the list with the last output', () => {
    const { onEdit } = renderPart(Outputs, runStep);
    fireEvent.click(
      screen.getByRole('button', { name: 'Remove output artifact' })
    );
    expect(onEdit).toHaveBeenCalledWith(fieldOp(['outputs'], undefined));
  });

  it('appends an output', () => {
    const { onEdit } = renderPart(Outputs, runStep);
    fireEvent.change(screen.getByLabelText('New output name'), {
      target: { value: 'report' },
    });
    fireEvent.keyDown(screen.getByLabelText('New output name'), {
      key: 'Enter',
    });
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['outputs', 1], { name: 'report' })
    );
  });

  // Enter of an input method confirms its text, not the new output.
  it('appends nothing on Enter while an input method composes', () => {
    const { onEdit } = renderPart(Outputs, runStep);
    const field = screen.getByLabelText('New output name');
    fireEvent.change(field, { target: { value: 'report' } });
    fireEvent.keyDown(field, { key: 'Enter', isComposing: true });
    expect(onEdit).not.toHaveBeenCalled();
    expect(field).toHaveValue('report');
  });

  it('gives a step with no id its name as id with the first output', () => {
    const { onEdit } = renderPart(Outputs, {
      ...runStep,
      explicitName: 'fetch data',
      id: undefined,
      value: {},
    });
    fireEvent.change(screen.getByLabelText('New output name'), {
      target: { value: 'report' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add output' }));
    expect(onEdit).toHaveBeenNthCalledWith(1, {
      kind: 'setStepId',
      step: runStep.key,
      id: 'fetch_data',
    });
    expect(onEdit).toHaveBeenNthCalledWith(
      2,
      fieldOp(['outputs'], [{ name: 'report' }])
    );
  });

  // An unnamed step has the name that the server gives it.
  it('gives an unnamed step the name of the server as id', () => {
    const { onEdit } = renderPart(
      Outputs,
      { ...runStep, explicitName: undefined, id: undefined, value: {} },
      { derivedName: 'cmd_1' }
    );
    fireEvent.change(screen.getByLabelText('New output name'), {
      target: { value: 'report' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add output' }));
    expect(onEdit).toHaveBeenNthCalledWith(1, {
      kind: 'setStepId',
      step: runStep.key,
      id: 'cmd_1',
    });
  });

  it('keeps the id of a step that has one', () => {
    const { onEdit } = renderPart(Outputs, { ...runStep, id: 'build_id' });
    fireEvent.change(screen.getByLabelText('New output name'), {
      target: { value: 'report' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add output' }));
    expect(onEdit).toHaveBeenCalledTimes(1);
  });

  it('creates the list for the first output', () => {
    const { onEdit } = renderPart(Outputs, {
      ...runStep,
      id: 'build_id',
      value: {},
    });
    fireEvent.change(screen.getByLabelText('New output name'), {
      target: { value: 'report' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add output' }));
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['outputs'], [{ name: 'report' }])
    );
  });

  it('emits nothing when read-only', () => {
    const { onEdit, edit } = renderPart(Outputs, runStep, { readOnly: true });
    edit('Output name', 'x');
    expect(screen.queryByLabelText('New output name')).toBeNull();
    expect(onEdit).not.toHaveBeenCalled();
  });
});
