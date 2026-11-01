// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { I18nText } from '@/i18n/I18nText';
import { TextField } from '../../TextField';
import type { FieldPath, SectionProps } from '../../types';

/**
 * Value of a number field that also takes a `${...}` reference: an empty
 * text removes the key, digits give a number, other text stays a string
 * (the validation reports a wrong value).
 */
export function numberOrText(text: string): number | string | undefined {
  const value = text.trim();
  if (value === '') {
    return undefined;
  }
  return /^\d+$/.test(value) ? Number(value) : value;
}

function shown(value: unknown): string {
  return typeof value === 'number' || typeof value === 'string'
    ? String(value)
    : '';
}

/** Timeout and retry policy of a step: one `setStepField` per changed key. */
export function Timing({
  step,
  readOnly,
  onEdit,
  fieldIssues,
}: SectionProps): ReactElement {
  const retry =
    typeof step.value.retry_policy === 'object' &&
    step.value.retry_policy !== null
      ? (step.value.retry_policy as Record<string, unknown>)
      : {};
  const field = (
    path: FieldPath,
    label: string,
    value: unknown,
    issueKey: string
  ) => (
    <div data-field-path={issueKey}>
      <TextField
        label={<I18nText text={label} />}
        value={shown(value)}
        readOnly={readOnly}
        issues={fieldIssues[issueKey]}
        onCommit={(text) =>
          onEdit({
            kind: 'setStepField',
            step: step.key,
            path,
            value: numberOrText(text),
          })
        }
      />
    </div>
  );
  return (
    <div className="space-y-1">
      {field(
        ['timeout_sec'],
        'Timeout (seconds)',
        step.value.timeout_sec,
        'timeout_sec'
      )}
      <div className="grid grid-cols-2 gap-2" data-field-path="retry_policy">
        {field(
          ['retry_policy', 'limit'],
          'Retry limit',
          retry.limit,
          'retry_policy.limit'
        )}
        {field(
          ['retry_policy', 'interval_sec'],
          'Retry interval (seconds)',
          retry.interval_sec,
          'retry_policy.interval_sec'
        )}
      </div>
    </div>
  );
}
