// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { FlowSection } from '../sections/Flow';
import { runStep } from './fixtures';
import { fieldOp, renderPart } from './flowHarness';
import {
  candidatesSample,
  referenceList,
  typeOpenReference,
} from './referenceHarness';

describe('FlowSection', () => {
  // Spec 006: a step env entry reads only the entries before it.
  it('offers an env row the entries before it only', () => {
    const entry = (name: string) => ({
      token: `\${env.${name}}`,
      label: name,
      group: 'stepEnv' as const,
      known: 'static' as const,
    });
    renderPart(
      FlowSection,
      { ...runStep, value: { env: [{ A: '1' }, { B: '2' }, { C: '3' }] } },
      { candidates: [entry('A'), entry('B'), entry('C'), ...candidatesSample] }
    );
    const env = screen.getByRole('group', { name: 'Environment' });
    typeOpenReference(within(env).getAllByLabelText('Value')[1]!);
    const list = referenceList();
    expect(list).toHaveTextContent('${env.A}');
    expect(list).toHaveTextContent('${params.API_URL}');
    expect(list).not.toHaveTextContent('${env.B}');
    expect(list).not.toHaveTextContent('${env.C}');
  });

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

  it('offers references in an environment value', () => {
    renderPart(FlowSection, runStep, { candidates: candidatesSample });
    typeOpenReference(screen.getByDisplayValue('-mod=mod'));
    expect(referenceList()).toBeInTheDocument();
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
