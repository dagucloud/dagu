// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus, X } from 'lucide-react';
import { useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { issuesUnder } from '../../issues';
import { FieldIssues } from '../../FieldIssues';
import { TextField } from '../../TextField';
import type { SectionProps } from '../../types';

type Condition = Record<string, unknown>;

/**
 * The conditions when the value is a list of condition maps, else null. A
 * map with `eval` is not a row: the server refuses `condition` next to it.
 */
export function conditionRows(value: unknown): Condition[] | null {
  if (value === undefined) {
    return [];
  }
  if (!Array.isArray(value)) {
    return null;
  }
  return value.every(
    (item) =>
      typeof item === 'object' &&
      item !== null &&
      !Array.isArray(item) &&
      !('eval' in item)
  )
    ? (value as Condition[])
    : null;
}

/**
 * Preconditions of a step as rows of `condition` and `expected`. A row
 * edit sets one key of one item, a removed row removes the item and a new
 * row appends one item. Another form is shown read-only.
 */
export function Preconditions({
  step,
  readOnly,
  onEdit,
  fieldIssues,
}: SectionProps): ReactElement {
  const { ts } = useI18n();
  const [condition, setCondition] = useState('');
  const [expected, setExpected] = useState('');
  const value = step.value.preconditions;
  const rows = conditionRows(value);
  const set = (path: (string | number)[], next: unknown) =>
    onEdit({
      kind: 'setStepField',
      step: step.key,
      path: ['preconditions', ...path],
      value: next,
    });

  const title = (
    <div className="text-xs font-medium text-muted-foreground">
      <I18nText text="Preconditions" />
    </div>
  );
  if (!rows) {
    return (
      <div className="space-y-0.5" data-field-path="preconditions">
        {title}
        <pre className="max-h-24 overflow-auto rounded bg-muted p-1 text-xs">
          {typeof value === 'string' ? value : JSON.stringify(value, null, 2)}
        </pre>
        <p className="text-xs text-muted-foreground">
          <I18nText text="Edit this field in YAML" />
        </p>
        <FieldIssues issues={issuesUnder(fieldIssues, 'preconditions')} />
      </div>
    );
  }

  const add = () => {
    const item: Condition = { condition: condition.trim() };
    if (expected.trim()) {
      item.expected = expected.trim();
    }
    if (value === undefined) {
      onEdit({
        kind: 'setStepField',
        step: step.key,
        path: ['preconditions'],
        value: [item],
      });
    } else {
      set([rows.length], item);
    }
    setCondition('');
    setExpected('');
  };

  return (
    <div className="space-y-1" data-field-path="preconditions">
      {title}
      {rows.map((row, index) => (
        <div
          key={index}
          className="flex items-end gap-1"
          data-field-path={`preconditions[${index}]`}
        >
          <div className="flex-[2]">
            <TextField
              label={<I18nText text="Condition" />}
              value={typeof row.condition === 'string' ? row.condition : ''}
              readOnly={readOnly}
              issues={fieldIssues[`preconditions[${index}].condition`]}
              onCommit={(text) => set([index, 'condition'], text)}
            />
          </div>
          <div className="flex-1">
            <TextField
              label={<I18nText text="Expected" />}
              value={typeof row.expected === 'string' ? row.expected : ''}
              readOnly={readOnly}
              onCommit={(text) =>
                set([index, 'expected'], text === '' ? undefined : text)
              }
            />
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={ts('Remove condition {index}', { index: index + 1 })}
            disabled={readOnly}
            onClick={() => set([index], undefined)}
          >
            <X />
          </Button>
        </div>
      ))}
      {!readOnly && (
        <div className="flex items-center gap-1">
          <Input
            className="h-7 flex-[2] px-2 text-xs"
            aria-label={ts('New condition')}
            placeholder={ts('Condition')}
            value={condition}
            onChange={(event) => setCondition(event.target.value)}
          />
          <Input
            className="h-7 flex-1 px-2 text-xs"
            aria-label={ts('New expected value')}
            placeholder={ts('Expected')}
            value={expected}
            onChange={(event) => setExpected(event.target.value)}
          />
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={ts('Add condition')}
            disabled={!condition.trim()}
            onClick={add}
          >
            <Plus />
          </Button>
        </div>
      )}
      <FieldIssues issues={fieldIssues.preconditions} />
    </div>
  );
}
