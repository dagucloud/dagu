// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type { StepInspectorProps, ValidationIssue } from '../../contracts';
import { catalogSample, modelArraySteps } from '../../testing/fixtures';
import { StepInspector } from '../StepInspector';
import { modelHttpJq, schemaSample } from './fixtures';

// The sections have their own tests: here they only show the props they get.
function stub(name: string) {
  return (props: {
    readOnly: boolean;
    fieldIssues?: Record<string, unknown>;
  }) => (
    <div
      data-testid={`section-${name}`}
      data-readonly={String(props.readOnly)}
      data-fields={Object.keys(props.fieldIssues ?? {}).join(',')}
    />
  );
}
vi.mock('../sections/General', () => ({ GeneralSection: stub('General') }));
vi.mock('../sections/Flow', () => ({ FlowSection: stub('Flow') }));
vi.mock('../sections/Parameters', () => ({
  ParametersSection: stub('Parameters'),
}));
vi.mock('../sections/StepYaml', () => ({ StepYamlSection: stub('StepYaml') }));
vi.mock('../sections/Action', () => ({
  ActionSection: stub('Action'),
  isRunStep: (step: { kind: string }) => step.kind === 'run',
}));

const issue = (fields: Partial<ValidationIssue>): ValidationIssue => ({
  severity: ValidationIssueSeverity.error,
  source: ValidationIssueSource.build,
  message: 'problem',
  ...fields,
});

function setup(props: Partial<StepInspectorProps> = {}) {
  const onEdit = vi.fn();
  const onClose = vi.fn();
  render(
    <StepInspector
      model={modelHttpJq}
      step="steps[0]"
      schema={schemaSample}
      catalog={catalogSample}
      customActions={[]}
      candidates={[]}
      issues={[]}
      readOnly={false}
      onEdit={onEdit}
      onClose={onClose}
      {...props}
    />
  );
  return { onEdit, onClose };
}

describe('StepInspector', () => {
  it('shows the step label, kind and sections', () => {
    setup();
    expect(screen.getByRole('heading', { name: 'fetch' })).toBeInTheDocument();
    expect(screen.getByText('action')).toBeInTheDocument();
    for (const name of ['General', 'Action', 'Parameters', 'Flow']) {
      expect(screen.getByTestId(`section-${name}`)).toHaveAttribute(
        'data-readonly',
        'false'
      );
    }
    // The YAML section starts closed.
    expect(screen.queryByTestId('section-StepYaml')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'YAML' }));
    expect(screen.getByTestId('section-StepYaml')).toBeInTheDocument();
  });

  it('calls onClose from the close button', () => {
    const { onClose } = setup();
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('counts the issues and puts the general ones at the top', () => {
    setup({
      issues: [
        issue({ stepPath: 'steps[0]', message: 'step problem' }),
        issue({ stepPath: 'steps[0]', fieldPath: 'steps[0].with.url' }),
      ],
    });
    expect(screen.getByLabelText('2 issues')).toHaveTextContent('2');
    expect(screen.getByRole('alert')).toHaveTextContent('step problem');
    expect(screen.getByTestId('section-Parameters')).toHaveAttribute(
      'data-fields',
      'with.url'
    );
    expect(
      screen.getByRole('button', { name: /Parameters/ })
    ).toHaveTextContent('1');
  });

  // The build error of a missing key goes to the field, not to the top.
  it('moves a required-field build error to its field', () => {
    setup({
      issues: [
        issue({
          stepPath: 'steps[0]',
          fieldPath: 'steps[0].with',
          message:
            "field 'steps': field 'with': with.url is required (value: map[a:b])",
        }),
      ],
    });
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByTestId('section-Parameters')).toHaveAttribute(
      'data-fields',
      'with.url'
    );
  });

  it('shows no parameters section for a command step', () => {
    setup({ model: modelArraySteps, step: 'steps[1]' });
    expect(
      screen.getByRole('heading', { name: 'transform' })
    ).toBeInTheDocument();
    expect(screen.getByText('run')).toBeInTheDocument();
    expect(screen.queryByTestId('section-Parameters')).toBeNull();
  });

  it('tells when the step no longer exists', () => {
    const { onClose } = setup({ step: 'steps[9]' });
    expect(screen.getByText('This step no longer exists.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalled();
  });

  it('passes readOnly to every section', () => {
    setup({ readOnly: true });
    expect(screen.getByTestId('section-General')).toHaveAttribute(
      'data-readonly',
      'true'
    );
    expect(screen.getByTestId('section-Flow')).toHaveAttribute(
      'data-readonly',
      'true'
    );
  });

  it('is read-only with a banner when the YAML does not parse', () => {
    setup({ model: { ...modelHttpJq, ok: false } });
    expect(screen.getByRole('status')).toHaveTextContent(
      'Fix the YAML errors to edit this step.'
    );
    expect(screen.getByTestId('section-General')).toHaveAttribute(
      'data-readonly',
      'true'
    );
  });

  it('renders the run panel slot', () => {
    setup({ runPanel: <div data-testid="run-panel" /> });
    expect(screen.getByTestId('run-panel')).toBeInTheDocument();
  });

  it('finds a handler step', () => {
    const handler = {
      ...modelHttpJq.steps[0]!,
      key: 'handler_on.failure',
      index: null,
      handler: 'failure' as const,
      explicitName: 'notify',
    };
    setup({
      model: { ...modelHttpJq, handlers: [handler] },
      step: 'handler_on.failure',
    });
    expect(screen.getByRole('heading', { name: 'notify' })).toBeInTheDocument();
    expect(screen.getByText('failure')).toBeInTheDocument();
  });
});
