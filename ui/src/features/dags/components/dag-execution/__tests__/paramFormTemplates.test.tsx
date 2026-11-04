// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import Form from '@rjsf/shadcn';
import type { RJSFSchema } from '@rjsf/utils';
import validator from '@rjsf/validator-ajv8';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { paramFormTemplates } from '../paramFormTemplates';
import { paramFormWidgets } from '../schemaFormWidgets';

function renderParamForm(schema: RJSFSchema) {
  render(
    <Form
      tagName="div"
      schema={schema}
      validator={validator}
      templates={paramFormTemplates}
      widgets={paramFormWidgets}
      uiSchema={{ 'ui:submitButtonOptions': { norender: true } }}
      noHtml5Validate
      showErrorList={false}
    />
  );
}

function expectBefore(first: Element, second: Element) {
  expect(
    first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING
  ).toBeTruthy();
}

describe('paramFormTemplates', () => {
  it('shows the description before the input', () => {
    renderParamForm({
      type: 'object',
      properties: {
        notes: { type: 'string', description: 'Free-form operator notes.' },
      },
    });

    expectBefore(
      screen.getByText('Free-form operator notes.'),
      screen.getByLabelText('notes')
    );
  });

  it('labels a titled field by its title and keeps its name visible', () => {
    renderParamForm({
      type: 'object',
      properties: {
        region: { type: 'string', title: 'AWS region' },
      },
    });

    expect(screen.getByLabelText('AWS region')).toBeInTheDocument();
    expect(screen.getByText('region')).toBeInTheDocument();
  });

  it('keeps the name of a titled boolean visible', () => {
    renderParamForm({
      type: 'object',
      properties: {
        dry_run: { type: 'boolean', title: 'Dry run' },
      },
    });

    expect(
      screen.getByRole('checkbox', { name: 'Dry run' })
    ).toBeInTheDocument();
    expect(screen.getByText('dry_run')).toBeInTheDocument();
  });
});
