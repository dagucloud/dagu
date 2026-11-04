// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { FieldTemplateProps, TemplatesType } from '@rjsf/utils';
import { getTemplate, getUiOptions, titleId } from '@rjsf/utils';

import { cn } from '@/lib/utils';
import { ParamLabel } from './ParamLabel';
import { schemaFormTemplates } from './schemaFormTemplates';

/**
 * Lays out a start parameter as label, description, then control, so the
 * guidance is read before the value is entered. A titled parameter also
 * shows its name.
 */
function ParamFieldTemplate({
  id,
  fieldPathId,
  children,
  displayLabel,
  rawErrors = [],
  errors,
  help,
  description,
  rawDescription,
  classNames,
  style,
  disabled,
  label,
  hidden,
  onKeyRename,
  onKeyRenameBlur,
  onRemoveProperty,
  readonly,
  required,
  schema,
  uiSchema,
  registry,
}: FieldTemplateProps) {
  const uiOptions = getUiOptions(uiSchema);
  const WrapIfAdditionalTemplate = getTemplate(
    'WrapIfAdditionalTemplate',
    registry,
    uiOptions
  );

  if (hidden) {
    return <div className="hidden">{children}</div>;
  }

  // Checkbox widgets render their own label and description.
  const showLabel = displayLabel && uiOptions.widget !== 'checkbox';
  const hasErrors = rawErrors.length > 0;
  const name = fieldPathId.path[fieldPathId.path.length - 1];

  return (
    <WrapIfAdditionalTemplate
      classNames={classNames}
      style={style}
      disabled={disabled}
      id={id}
      label={label}
      displayLabel={displayLabel}
      onKeyRename={onKeyRename}
      onKeyRenameBlur={onKeyRenameBlur}
      onRemoveProperty={onRemoveProperty}
      rawDescription={rawDescription}
      readonly={readonly}
      required={required}
      schema={schema}
      uiSchema={uiSchema}
      registry={registry}
    >
      <div className="flex flex-col gap-2">
        {showLabel && (
          <ParamLabel
            id={titleId(id)}
            htmlFor={id}
            name={typeof name === 'string' ? name : undefined}
            label={label}
            className={cn('leading-none', hasErrors && 'text-destructive')}
          >
            {label}
            {required ? '*' : null}
          </ParamLabel>
        )}
        {showLabel && rawDescription && (
          <div
            className={cn(
              'text-xs font-medium text-muted-foreground',
              hasErrors && 'text-destructive'
            )}
          >
            {description}
          </div>
        )}
        {children}
        {errors}
        {help}
      </div>
    </WrapIfAdditionalTemplate>
  );
}

/** Templates for schema-backed start parameter forms. */
export const paramFormTemplates: Partial<TemplatesType> = {
  ...schemaFormTemplates,
  FieldTemplate: ParamFieldTemplate,
};
