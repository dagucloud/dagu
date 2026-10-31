// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import Form from '@rjsf/shadcn';
import type { RJSFSchema, UiSchema } from '@rjsf/utils';
import validator from '@rjsf/validator-ajv8';
import { render } from '@testing-library/react';
import { vi } from 'vitest';
import { schemaFormTemplates } from '@/features/dags/components/dag-execution/schemaFormTemplates';
import type { JSONSchema, ReferenceCandidate } from '../../contracts';
import {
  buildUiSchema,
  inspectorFields,
  inspectorTemplates,
  inspectorWidgets,
} from '../widgets';

/**
 * Renders an rjsf form with the inspector widgets, fields and templates.
 * `onChange` receives the form data of each change.
 */
export function renderInspectorForm(
  schema: JSONSchema,
  formData: unknown,
  options: {
    disabled?: boolean;
    uiSchema?: UiSchema;
    candidates?: ReferenceCandidate[];
  } = {}
) {
  const onChange = vi.fn();
  const view = render(
    <Form
      tagName="div"
      schema={schema as RJSFSchema}
      formData={formData}
      validator={validator}
      widgets={inspectorWidgets}
      fields={inspectorFields}
      templates={{ ...schemaFormTemplates, ...inspectorTemplates }}
      uiSchema={{
        ...(options.uiSchema ?? buildUiSchema(schema)),
        'ui:submitButtonOptions': { norender: true },
      }}
      formContext={{ candidates: options.candidates ?? [] }}
      disabled={options.disabled}
      noHtml5Validate
      showErrorList={false}
      onChange={(event) => onChange(event.formData)}
    />
  );
  return { ...view, onChange };
}
