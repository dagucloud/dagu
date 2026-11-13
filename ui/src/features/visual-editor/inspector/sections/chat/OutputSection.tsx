// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useId, useMemo, useState, type ReactElement } from 'react';
import { parseDocument, stringify } from 'yaml';
import { Switch } from '@/components/ui/switch';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { FieldIssues } from '../../FieldIssues';
import { sameValue } from '../../formDiff';
import { issuesUnder } from '../../issues';
import { isPlainObject } from '../../paramsForm';
import { MonacoField } from '../../MonacoField';
import type { SectionProps } from '../../types';
import { makeSetField, YamlOnlyValue } from './fields';
import { OUTPUT_KEY } from './model';

/** Lines that the schema editor shows. */
const SCHEMA_LINES = 10;

/** What the switch writes: a schema with one text property, to edit. */
export const STARTER_SCHEMA = {
  type: 'object',
  properties: { answer: { type: 'string' } },
} as const;

interface SchemaCheck {
  value?: Record<string, unknown>;
  error?: { message: string; line?: number; column?: number };
}

/** Parses the text of the schema editor: it must be one YAML mapping. */
export function parseSchemaText(text: string): SchemaCheck {
  const doc = parseDocument(text);
  const [first] = doc.errors;
  if (first) {
    const pos = first.linePos?.[0];
    const message = first.message
      .split('\n')[0]!
      .replace(/ at line \d+, column \d+:?$/, '');
    return { error: { message, line: pos?.line, column: pos?.col } };
  }
  const value: unknown = doc.toJS();
  if (!isPlainObject(value)) {
    return { error: { message: 'The output format must be a YAML mapping.' } };
  }
  return { value };
}

/**
 * Output format of a chat step: the step-level `output_schema`. A switch
 * adds a schema or removes it; the schema is YAML in a multi-line editor
 * that sends its text on blur. A text with a syntax error shows the error
 * and sends nothing.
 */
export function OutputSection({
  step,
  readOnly,
  onEdit,
  fieldIssues,
}: SectionProps): ReactElement {
  const { ts } = useI18n();
  const id = useId();
  const set = makeSetField(step.key, onEdit);
  const schema = step.value[OUTPUT_KEY];
  const on = schema !== undefined && schema !== null;
  const editable = isPlainObject(schema);
  const text = useMemo(
    () => (editable ? stringify(schema).trimEnd() : ''),
    [editable, schema]
  );
  const [error, setError] = useState<SchemaCheck['error'] | null>(null);
  return (
    <div className="space-y-2" data-field-path={OUTPUT_KEY}>
      <div className="flex items-center justify-between gap-2">
        <label
          htmlFor={id}
          className="text-xs font-medium text-muted-foreground"
        >
          <I18nText text="Require a specific output format" />
        </label>
        <Switch
          id={id}
          checked={on}
          disabled={readOnly}
          onCheckedChange={(checked) => {
            setError(null);
            set([OUTPUT_KEY], checked ? STARTER_SCHEMA : undefined);
          }}
        />
      </div>
      <p className="text-[11px] text-muted-foreground">
        <I18nText text="The model answers in the shape of a JSON schema, written as YAML. Only the listed properties are published." />
      </p>
      {on &&
        (editable ? (
          <div className="space-y-1">
            <MonacoField
              value={text}
              language="yaml"
              lines={SCHEMA_LINES}
              readOnly={readOnly}
              ariaLabel={ts('Output format')}
              onChange={() => setError(null)}
              onCommit={(next) => {
                const check = parseSchemaText(next);
                setError(check.error ?? null);
                if (check.value && !sameValue(check.value, schema)) {
                  set([OUTPUT_KEY], check.value);
                }
              }}
            />
            {error && (
              <p role="alert" className="text-xs text-destructive">
                {error.line !== undefined
                  ? ts('Line {line}, column {column}: {message}', {
                      line: error.line,
                      column: error.column ?? 1,
                      message: ts(error.message),
                    })
                  : ts(error.message)}
              </p>
            )}
          </div>
        ) : (
          <YamlOnlyValue name={OUTPUT_KEY} value={schema} />
        ))}
      <FieldIssues issues={issuesUnder(fieldIssues, OUTPUT_KEY)} />
    </div>
  );
}
