// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useMemo, useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import type {
  DagDocModel,
  DagEditOp,
  JSONSchema,
  StepView,
} from '../../contracts';
import { MonacoField } from '../MonacoField';
import { checkStepSchema } from '../stepSchemaCheck';
import {
  parseStepYaml,
  stepYamlFromModel,
  type StepYamlCheck,
} from '../stepYamlText';

export interface StepYamlSectionProps {
  model: DagDocModel;
  step: StepView;
  schema: JSONSchema;
  readOnly: boolean;
  onEdit(op: DagEditOp): void;
}

/**
 * YAML of one step in a small editor. "Apply" parses the text: a syntax
 * error, or a text that is not a mapping, shows the error and sends
 * nothing. A valid text sends one `replaceStepYaml` with the text at
 * column 0; messages of the DAG schema show as warnings and do not stop
 * the edit.
 */
export function StepYamlSection({
  model,
  step,
  schema,
  readOnly,
  onEdit,
}: StepYamlSectionProps): ReactElement {
  const { ts } = useI18n();
  const original = useMemo(() => stepYamlFromModel(model, step), [model, step]);
  const [text, setText] = useState(original);
  const [error, setError] = useState<StepYamlCheck['error'] | null>(null);
  const [warnings, setWarnings] = useState<string[]>([]);

  useEffect(() => {
    setText(original);
    setError(null);
  }, [original]);

  const dirty = text !== original;
  const apply = () => {
    const check = parseStepYaml(text);
    if (!check.ok) {
      setError(check.error ?? null);
      setWarnings([]);
      return;
    }
    setError(null);
    setWarnings(checkStepSchema(schema, check.value));
    onEdit({ kind: 'replaceStepYaml', step: step.key, yaml: text });
  };

  return (
    <div className="space-y-1">
      <MonacoField
        value={text}
        language="yaml"
        lines={12}
        readOnly={readOnly}
        ariaLabel={ts('Step YAML')}
        onChange={(next) => {
          setText(next);
          setError(null);
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
      {warnings.length > 0 && (
        <ul
          className="space-y-0.5 text-xs text-warning"
          aria-label={ts('Schema warnings')}
        >
          {warnings.map((warning) => (
            <li key={warning} className="break-words">
              {warning}
            </li>
          ))}
        </ul>
      )}
      {!readOnly && (
        <div className="flex justify-end gap-1">
          <Button
            type="button"
            size="xs"
            variant="ghost"
            disabled={!dirty}
            onClick={() => {
              setText(original);
              setError(null);
              setWarnings([]);
            }}
          >
            <I18nText text="Reset" />
          </Button>
          <Button
            type="button"
            size="xs"
            variant="primary"
            disabled={!dirty}
            onClick={apply}
          >
            <I18nText text="Apply" />
          </Button>
        </div>
      )}
    </div>
  );
}
