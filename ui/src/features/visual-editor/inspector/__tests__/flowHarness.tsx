// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import type { ComponentType } from 'react';
import { vi } from 'vitest';
import type { StepView, ValidationIssue } from '../../contracts';
import type { SectionProps } from '../types';

/** Renders one flow part with an `onEdit` mock and a helper that edits a text field. */
export function renderPart(
  Part: ComponentType<SectionProps>,
  step: StepView,
  options: {
    readOnly?: boolean;
    fieldIssues?: Record<string, ValidationIssue[]>;
    derivedName?: string;
  } = {}
) {
  const onEdit = vi.fn();
  const view = render(
    <Part
      step={step}
      readOnly={options.readOnly ?? false}
      onEdit={onEdit}
      fieldIssues={options.fieldIssues ?? {}}
      derivedName={options.derivedName}
    />
  );
  const edit = (label: string, value: string, index = 0) => {
    const input = screen.getAllByLabelText(label)[index]!;
    fireEvent.change(input, { target: { value } });
    fireEvent.blur(input);
  };
  return { ...view, onEdit, edit };
}

export const fieldOp = (path: (string | number)[], value: unknown) => ({
  kind: 'setStepField',
  step: 'steps[2]',
  path,
  value,
});
