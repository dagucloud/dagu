// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import type { ReferenceCandidate } from '../../contracts';
import { MonacoField } from '../MonacoField';
import type { StepYamlCheck } from '../stepYamlText';

export interface YamlBoxProps {
  /** The YAML text from the file. The box shows it again when it changes. */
  original: string;
  readOnly: boolean;
  ariaLabel: string;
  /** Parses the text; a text that does not parse is not applied. */
  check(text: string): StepYamlCheck;
  /** Schema messages for a text that parses; they do not stop the edit. */
  warnings?(value: Record<string, unknown>): string[];
  /** Applies a text that parses. */
  onApply(text: string): void;
  /** The references that a field can offer, by its path in the text. */
  candidatesAtPath?(path: string[]): ReferenceCandidate[];
}

/**
 * YAML of one part of a step in a small editor: the step or one tool entry.
 * "Apply" parses the text: a syntax error, or a text that is not a mapping,
 * shows the error and sends nothing. A valid text goes to `onApply`; messages
 * of the schema show as warnings and do not stop the edit.
 */
export function YamlBox({
  original,
  readOnly,
  ariaLabel,
  check,
  warnings: schemaWarnings,
  onApply,
  candidatesAtPath,
}: YamlBoxProps): ReactElement {
  const { ts } = useI18n();
  const [text, setText] = useState(original);
  const [error, setError] = useState<StepYamlCheck['error'] | null>(null);
  const [warnings, setWarnings] = useState<string[]>([]);

  useEffect(() => {
    setText(original);
    setError(null);
  }, [original]);

  const dirty = text !== original;
  const apply = () => {
    const checked = check(text);
    if (!checked.ok) {
      setError(checked.error ?? null);
      setWarnings([]);
      return;
    }
    setError(null);
    setWarnings(
      checked.value && schemaWarnings ? schemaWarnings(checked.value) : []
    );
    onApply(text);
  };

  return (
    <div className="space-y-1">
      <MonacoField
        value={text}
        language="yaml"
        lines={12}
        readOnly={readOnly}
        ariaLabel={ariaLabel}
        candidatesAtPath={candidatesAtPath}
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
