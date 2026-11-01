// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { WidgetProps } from '@rjsf/utils';
import { getTemplate } from '@rjsf/utils';
import type { ReactElement } from 'react';
import { ReferenceInput } from '@/features/visual-editor/references';
import type { ReferenceCandidate } from '../../contracts';

/** Reference candidates of the `formContext` of the parameters form. */
export function contextCandidates(formContext: unknown): ReferenceCandidate[] {
  const candidates = (formContext as { candidates?: unknown } | undefined)
    ?.candidates;
  return Array.isArray(candidates) ? (candidates as ReferenceCandidate[]) : [];
}

function isStringSchema(schema: WidgetProps['schema']): boolean {
  const type = schema.type;
  return (
    type === undefined ||
    type === 'string' ||
    (Array.isArray(type) && type.includes('string'))
  );
}

function textOf(value: unknown): string {
  if (value === undefined || value === null) {
    return '';
  }
  return typeof value === 'string' ? value : String(value);
}

function ReferenceWidget(props: WidgetProps, multiline: boolean): ReactElement {
  const {
    id,
    value,
    onChange,
    onBlur,
    disabled,
    readonly,
    label,
    schema,
    registry,
    options,
    placeholder,
  } = props;
  if (!isStringSchema(schema)) {
    // Number fields also use the text widget: keep the default input.
    const BaseInputTemplate = getTemplate(
      'BaseInputTemplate',
      registry,
      options
    );
    return <BaseInputTemplate {...props} />;
  }
  return (
    <ReferenceInput
      id={id}
      value={textOf(value)}
      onChange={(text) => onChange(text === '' ? options.emptyValue : text)}
      onBlur={() => onBlur(id, value)}
      candidates={contextCandidates(registry.formContext)}
      disabled={disabled || readonly}
      aria-label={label}
      placeholder={placeholder}
      multiline={multiline}
    />
  );
}

/**
 * Text widget of the parameters form: a string field takes a value or a
 * `${...}` reference through `ReferenceInput`. Other types keep the default
 * input of the theme.
 */
export function ReferenceTextWidget(props: WidgetProps): ReactElement {
  return ReferenceWidget(props, false);
}

/** Multi-line form of `ReferenceTextWidget` for long text such as a body. */
export function ReferenceTextareaWidget(props: WidgetProps): ReactElement {
  return ReferenceWidget(props, true);
}
