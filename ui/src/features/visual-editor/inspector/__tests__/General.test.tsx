// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { StepView } from '../../contracts';
import { issuesSample } from '../../testing/fixtures';
import { GeneralSection } from '../sections/General';
import { httpStep } from './fixtures';

function setup(step: StepView = httpStep, readOnly = false) {
  const onEdit = vi.fn();
  render(
    <GeneralSection
      step={step}
      readOnly={readOnly}
      onEdit={onEdit}
      fieldIssues={{ name: [issuesSample[0]!] }}
    />
  );
  const edit = (label: string, value: string) => {
    const input = screen.getByLabelText(label);
    fireEvent.change(input, { target: { value } });
    fireEvent.blur(input);
  };
  return { onEdit, edit };
}

describe('GeneralSection', () => {
  it('renames the step', () => {
    const { onEdit, edit } = setup();
    edit('Name', ' load ');
    expect(onEdit).toHaveBeenCalledWith({
      kind: 'renameStep',
      step: 'steps[0]',
      newName: 'load',
    });
  });

  it.each(['', '   ', 'fetch'])('does not rename to %j', (value) => {
    const { onEdit, edit } = setup();
    edit('Name', value);
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('shows the derived name of an unnamed step as the placeholder', () => {
    render(
      <GeneralSection
        step={{ ...httpStep, explicitName: undefined }}
        readOnly={false}
        onEdit={() => undefined}
        fieldIssues={{}}
        derivedName="cmd_1"
      />
    );
    expect(screen.getByLabelText('Name')).toHaveAttribute(
      'placeholder',
      'cmd_1'
    );
  });

  // Each field is reachable by its label.
  it('labels the name, id and description fields', () => {
    setup();
    for (const label of ['Name', 'ID', 'Description']) {
      expect(screen.getByLabelText(label)).toBeInTheDocument();
    }
  });

  // The event gives a handler its name: the field shows it and is locked.
  it('shows the event name of a handler and does not edit it', () => {
    const handler = {
      ...httpStep,
      key: 'handler_on.success',
      handler: 'success',
    } as StepView;
    const { onEdit, edit } = setup({ ...handler, explicitName: undefined });
    const name = screen.getByLabelText('Name');
    expect(name).toHaveAttribute('placeholder', 'onSuccess');
    expect(name).toBeDisabled();
    edit('Name', 'other');
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('sets and removes the id', () => {
    const { onEdit, edit } = setup();
    edit('ID', 'load');
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepId',
      step: 'steps[0]',
      id: 'load',
    });
    edit('ID', '');
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepId',
      step: 'steps[0]',
      id: null,
    });
  });

  it('sets and removes the description', () => {
    const { onEdit, edit } = setup();
    edit('Description', 'Fetch the items.');
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: 'steps[0]',
      path: ['description'],
      value: 'Fetch the items.',
    });
    edit('Description', ' ');
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: 'steps[0]',
      path: ['description'],
      value: undefined,
    });
  });

  it('shows the step key as the placeholder of an unnamed step', () => {
    setup({ ...httpStep, explicitName: undefined });
    expect(screen.getByLabelText('Name')).toHaveAttribute(
      'placeholder',
      'steps[0]'
    );
  });

  it('shows the issues of the name field', () => {
    setup();
    expect(screen.getByRole('alert')).toHaveTextContent('unknown dependency');
  });

  it('sends nothing when read-only', () => {
    const { onEdit, edit } = setup(httpStep, true);
    edit('Name', 'load');
    edit('ID', '');
    expect(onEdit).not.toHaveBeenCalled();
    expect(screen.getByLabelText('ID')).toBeDisabled();
  });
});
