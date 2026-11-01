// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { FieldProps, RJSFSchema } from '@rjsf/utils';
import { useState, type ReactElement } from 'react';
import { ReferenceInput } from '@/features/visual-editor/references';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import { KeyValueEditor } from './KeyValueWidget';
import { contextCandidates } from './ReferenceTextWidget';

function branches(schema: RJSFSchema): RJSFSchema[] {
  return (schema.oneOf ?? schema.anyOf ?? []) as RJSFSchema[];
}

/**
 * True for a `oneOf`/`anyOf` that takes a string and a free object, such as
 * the `params` of `dag.run`: `"a=1 b=2"` or `{a: 1, b: 2}`. A list branch
 * may be there too.
 */
export function isStringOrMap(schema: RJSFSchema): boolean {
  const list = branches(schema);
  return (
    list.some((branch) => branch.type === 'string') &&
    list.some((branch) => branch.type === 'object' && !branch.properties)
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isStringMap(value: unknown): value is Record<string, string> {
  return (
    isRecord(value) &&
    Object.values(value).every((item) => typeof item === 'string')
  );
}

/**
 * rjsf field for a value that is a string or a map, with a toggle between
 * "Text" and "Key/value". The start mode follows the current value. A mode
 * change sends nothing until the user enters a value. A value that the two
 * modes cannot show, such as a list or a map with a number, shows as JSON
 * and is edited in the YAML section.
 */
export function StringOrMapField(props: FieldProps): ReactElement {
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
  const locked = Boolean(disabled || readonly);
  const [mode, setMode] = useState<'text' | 'map'>(
    typeof formData === 'string' ? 'text' : 'map'
  );
  const send = (value: unknown) => onChange(value, fieldPathId.path);
  const showable =
    formData === undefined ||
    typeof formData === 'string' ||
    isStringMap(formData);

  const toggle = (target: 'text' | 'map', text: string) => (
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
  if (!showable) {
    input = (
      <>
        <pre className="max-h-24 overflow-auto rounded bg-muted p-1 text-xs">
          {JSON.stringify(formData, null, 2)}
        </pre>
        <p className="text-xs text-muted-foreground">
          <I18nText text="Edit this field in YAML" />
        </p>
      </>
    );
  } else if (mode === 'text') {
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
  } else {
    input = (
      <KeyValueEditor
        label={label}
        value={isStringMap(formData) ? formData : {}}
        readOnly={locked}
        onChange={(next) =>
          send(Object.keys(next).length > 0 ? next : undefined)
        }
      />
    );
  }

  return (
    <div className="space-y-1">
      <div className="text-xs font-medium text-foreground">
        {label}
        {required ? <span className="text-destructive"> *</span> : null}
      </div>
      {schema.description ? (
        <p className="text-xs text-muted-foreground">{schema.description}</p>
      ) : null}
      {showable && (
        <div
          className="flex w-max overflow-hidden rounded border border-border"
          role="group"
        >
          {toggle('text', 'Text')}
          {toggle('map', 'Key/value')}
        </div>
      )}
      {input}
    </div>
  );
}
