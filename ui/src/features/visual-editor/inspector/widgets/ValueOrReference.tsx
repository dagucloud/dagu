// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { FieldProps, RJSFSchema } from '@rjsf/utils';
import { useState, type ReactElement } from 'react';
import { AutocompleteInput } from '@/components/ui/autocomplete-input';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { ReferenceInput } from '@/features/visual-editor/references';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import { contextCandidates } from './ReferenceTextWidget';

export type ScalarKind = 'integer' | 'number' | 'boolean';

function branchTypes(schema: RJSFSchema): string[] {
  const branches = (schema.oneOf ?? schema.anyOf ?? []) as RJSFSchema[];
  return branches.flatMap((branch) =>
    Array.isArray(branch.type) ? branch.type : branch.type ? [branch.type] : []
  );
}

/**
 * Kind of the value branch of a `oneOf`/`anyOf` of a scalar type and a
 * string, such as `oneOf: [{type: integer}, {type: string}]`. Gives null
 * for other schemas.
 */
export function scalarUnionKind(schema: RJSFSchema): ScalarKind | null {
  const types = branchTypes(schema);
  if (!types.includes('string')) {
    return null;
  }
  const others = types.filter((type) => type !== 'string');
  if (
    others.length === 0 ||
    !others.every((t) => ['integer', 'number', 'boolean'].includes(t))
  ) {
    return null;
  }
  return (others.includes('boolean') ? 'boolean' : others[0]) as ScalarKind;
}

/**
 * Values that the listed branches of a `oneOf`/`anyOf` of strings suggest,
 * such as `anyOf: [{type: string, enum: [claude, codex]}, {type: string}]`.
 * Gives null unless another branch takes any string.
 */
export function suggestedValues(schema: RJSFSchema): string[] | null {
  const branches = (schema.oneOf ?? schema.anyOf ?? []) as RJSFSchema[];
  if (
    branches.length === 0 ||
    !branches.every((branch) => branch.type === 'string')
  ) {
    return null;
  }
  const listed = branches.filter((branch) => Array.isArray(branch.enum));
  if (listed.length === 0 || listed.length === branches.length) {
    return null;
  }
  const values = listed.flatMap((branch) =>
    (branch.enum ?? []).filter(
      (value): value is string => typeof value === 'string'
    )
  );
  return [...new Set(values)];
}

function FieldLabel({
  label,
  required,
  description,
}: {
  label: string;
  required?: boolean;
  description?: string;
}) {
  return (
    <>
      <div className="text-xs font-medium text-foreground">
        {label}
        {required ? <span className="text-destructive"> *</span> : null}
      </div>
      {description ? (
        <p className="text-xs text-muted-foreground">{description}</p>
      ) : null}
    </>
  );
}

/**
 * rjsf field for a scalar that also takes a `${...}` reference: a toggle
 * between "Value" (a number input or a switch) and "Reference" (a
 * `ReferenceInput`). The start mode follows the current value: a string
 * is a reference. A mode change sends nothing until the user enters a value.
 */
export function ValueOrReferenceField(props: FieldProps): ReactElement {
  const {
    formData,
    onChange,
    fieldPathId,
    disabled,
    readonly,
    schema,
    name,
    required,
    registry,
  } = props;
  const kind = scalarUnionKind(schema) ?? 'integer';
  const [mode, setMode] = useState<'value' | 'reference'>(
    typeof formData === 'string' ? 'reference' : 'value'
  );
  const locked = Boolean(disabled || readonly);
  const label = schema.title ?? name;
  const send = (value: unknown) => onChange(value, fieldPathId.path);

  const toggle = (target: 'value' | 'reference', text: string) => (
    <button
      type="button"
      aria-pressed={mode === target}
      disabled={locked}
      onClick={() => setMode(target)}
      className={cn(
        'h-6 px-2 text-xs',
        mode === target
          ? 'bg-muted font-medium text-foreground'
          : 'text-muted-foreground'
      )}
    >
      <I18nText text={text} />
    </button>
  );

  let input: ReactElement;
  if (mode === 'reference') {
    input = (
      <ReferenceInput
        id={fieldPathId.$id}
        aria-label={label}
        value={typeof formData === 'string' ? formData : ''}
        candidates={contextCandidates(registry.formContext)}
        disabled={locked}
        onChange={(text) => send(text === '' ? undefined : text)}
      />
    );
  } else if (kind === 'boolean') {
    input = (
      <Switch
        id={fieldPathId.$id}
        aria-label={label}
        checked={formData === true}
        disabled={locked}
        onCheckedChange={(checked) => send(checked)}
      />
    );
  } else {
    input = (
      <Input
        id={fieldPathId.$id}
        aria-label={label}
        type="number"
        className="h-7 px-2 text-xs"
        step={kind === 'integer' ? 1 : 'any'}
        value={typeof formData === 'number' ? String(formData) : ''}
        disabled={locked}
        onChange={(event) => {
          const text = event.target.value;
          // A number input accepts 1e3. A fraction in an integer field
          // stays as typed, and the step check reports it.
          const number = Number(text);
          send(text === '' || Number.isNaN(number) ? undefined : number);
        }}
      />
    );
  }

  return (
    <div className="space-y-1">
      <FieldLabel
        label={label}
        required={required}
        description={schema.description}
      />
      <div className="flex items-center gap-1">
        <div
          className="flex overflow-hidden rounded border border-border"
          role="group"
        >
          {toggle('value', 'Value')}
          {toggle('reference', 'Reference')}
        </div>
        <div className="min-w-0 flex-1">{input}</div>
      </div>
    </div>
  );
}

/**
 * rjsf field for a property without a type, such as the `data` input of
 * `jq.filter`. A string value (or no value) takes a value or a reference;
 * another value shows as JSON and is edited in the YAML section.
 */
export function FreeValueField(props: FieldProps): ReactElement {
  const {
    formData,
    onChange,
    fieldPathId,
    disabled,
    readonly,
    schema,
    name,
    required,
    registry,
  } = props;
  const label = schema.title ?? name;
  const editable = formData === undefined || typeof formData === 'string';
  return (
    <div className="space-y-1">
      <FieldLabel
        label={label}
        required={required}
        description={schema.description}
      />
      {editable ? (
        <ReferenceInput
          id={fieldPathId.$id}
          aria-label={label}
          value={formData ?? ''}
          candidates={contextCandidates(registry.formContext)}
          disabled={Boolean(disabled || readonly)}
          onChange={(text) =>
            onChange(text === '' ? undefined : text, fieldPathId.path)
          }
        />
      ) : (
        <>
          <pre className="max-h-24 overflow-auto rounded bg-muted p-1 text-xs">
            {JSON.stringify(formData, null, 2)}
          </pre>
          <p className="text-xs text-muted-foreground">
            <I18nText text="Edit this field in YAML" />
          </p>
        </>
      )}
    </div>
  );
}

/**
 * rjsf field for a string that suggests the listed values of its union (see
 * `suggestedValues`) and takes any other text, such as a provider name that
 * is built-in or custom.
 */
export function SuggestedTextField(props: FieldProps): ReactElement {
  const {
    formData,
    onChange,
    fieldPathId,
    disabled,
    readonly,
    schema,
    name,
    required,
  } = props;
  const label = schema.title ?? name;
  return (
    <div className="space-y-1">
      <FieldLabel
        label={label}
        required={required}
        description={schema.description}
      />
      <AutocompleteInput
        id={fieldPathId.$id}
        aria-label={label}
        value={typeof formData === 'string' ? formData : ''}
        suggestions={suggestedValues(schema) ?? []}
        disabled={Boolean(disabled || readonly)}
        className="h-7 px-2 text-xs"
        onValueChange={(text) =>
          onChange(text === '' ? undefined : text, fieldPathId.path)
        }
      />
    </div>
  );
}
