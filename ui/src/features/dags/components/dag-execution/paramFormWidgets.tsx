// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { RegistryWidgetsType, WidgetProps } from '@rjsf/utils';
import type { ComponentType } from 'react';
import {
  ariaDescribedByIds,
  descriptionId,
  getTemplate,
  schemaRequiresTrueValue,
  titleId,
} from '@rjsf/utils';

import { Checkbox } from '@/components/ui/checkbox';
import { cn } from '@/lib/utils';
import { ParamLabel } from './ParamLabel';
import {
  schemaCheckboxClassName,
  schemaFormWidgets,
} from './schemaFormWidgets';

/**
 * Renders a boolean start parameter as a checkbox followed by its label,
 * with the description under the label, so the label is read first.
 */
function ParamCheckboxWidget({
  id,
  name,
  htmlName,
  value,
  disabled,
  readonly,
  label,
  hideLabel,
  schema,
  autofocus,
  options,
  onChange,
  onBlur,
  onFocus,
  registry,
  uiSchema,
  className,
  rawErrors = [],
}: WidgetProps) {
  const DescriptionFieldTemplate = getTemplate(
    'DescriptionFieldTemplate',
    registry,
    options
  );
  const description = options.description || schema.description;
  const showLabel = !hideLabel && !!label;
  const showDescription = !hideLabel && !!description;

  return (
    <div
      className={cn(
        'my-2 flex items-start gap-2 py-0.5',
        (disabled || readonly) && 'cursor-not-allowed opacity-50'
      )}
    >
      <Checkbox
        id={id}
        name={htmlName || id}
        checked={typeof value === 'undefined' ? false : Boolean(value)}
        required={schemaRequiresTrueValue(schema)}
        disabled={disabled || readonly}
        autoFocus={autofocus}
        aria-describedby={ariaDescribedByIds(id)}
        aria-invalid={rawErrors.length > 0 || undefined}
        onCheckedChange={(checked) => onChange(Boolean(checked))}
        onBlur={() => onBlur(id, value)}
        onFocus={() => onFocus(id, value)}
        className={cn(schemaCheckboxClassName, className)}
      />
      {(showLabel || showDescription) && (
        <div className="flex min-w-0 flex-col gap-1">
          {showLabel && (
            <ParamLabel
              htmlFor={id}
              name={name}
              label={label}
              className="leading-tight"
            >
              {label}
            </ParamLabel>
          )}
          {showDescription && (
            <DescriptionFieldTemplate
              id={descriptionId(id)}
              description={description}
              schema={schema}
              uiSchema={uiSchema}
              registry={registry}
            />
          )}
        </div>
      )}
    </div>
  );
}

const SchemaRadioWidget =
  schemaFormWidgets.RadioWidget as ComponentType<WidgetProps>;

/** Names a start parameter's radio group by the field label. */
function ParamRadioWidget(props: WidgetProps) {
  return (
    <SchemaRadioWidget
      {...props}
      options={{ ...props.options, labelledBy: titleId(props.id) }}
    />
  );
}

/** Widgets for schema-backed start parameter forms. */
export const paramFormWidgets: RegistryWidgetsType = {
  ...schemaFormWidgets,
  CheckboxWidget: ParamCheckboxWidget,
  RadioWidget: ParamRadioWidget,
};
