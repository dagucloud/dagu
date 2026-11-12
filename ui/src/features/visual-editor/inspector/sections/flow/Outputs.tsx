// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus, X } from 'lucide-react';
import { useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { validateStepId } from '../../../doc/names';
import { FieldIssues } from '../../FieldIssues';
import { issuesUnder } from '../../issues';
import { TextField } from '../../TextField';
import type { SectionProps } from '../../types';

function outputName(item: unknown): string {
  if (typeof item === 'object' && item !== null && 'name' in item) {
    return String((item as { name: unknown }).name ?? '');
  }
  return '';
}

/** A valid step id made from the name of a step, or null. */
function idFromName(name: string | undefined): string | null {
  if (!name) return null;
  const slug = name.replace(/[^A-Za-z0-9_]+/g, '_').replace(/^_+|_+$/g, '');
  const id = /^[A-Za-z]/.test(slug) ? slug : `step_${slug}`;
  return validateStepId(id) === null ? id : null;
}

/**
 * Names of the declared outputs of a step. The first output of a step with
 * no id also gives the step an id. A rename sets
 * `outputs[i].name`, a removed row removes the item (the last one removes
 * `outputs`: the server refuses an empty list) and a new name appends
 * `{ name }`.
 */
export function Outputs({
  step,
  readOnly,
  onEdit,
  fieldIssues,
  derivedName,
}: SectionProps): ReactElement {
  const { ts } = useI18n();
  const [name, setName] = useState('');
  const value = step.value.outputs;
  const items = Array.isArray(value) ? value : [];
  const set = (path: (string | number)[], next: unknown) =>
    onEdit({ kind: 'setStepField', step: step.key, path, value: next });

  const add = () => {
    const trimmed = name.trim();
    if (!trimmed) {
      return;
    }
    // A step with outputs must define an id: it takes its name, written or
    // given by the server, as id.
    const id = step.id ? null : idFromName(step.explicitName ?? derivedName);
    if (id !== null) onEdit({ kind: 'setStepId', step: step.key, id });
    if (Array.isArray(value)) {
      set(['outputs', items.length], { name: trimmed });
    } else {
      set(['outputs'], [{ name: trimmed }]);
    }
    setName('');
  };

  return (
    <div className="space-y-1" data-field-path="outputs">
      <div className="text-xs font-medium text-muted-foreground">
        <I18nText text="Outputs" />
      </div>
      {items.map((item, index) => (
        <div
          key={index}
          className="flex items-end gap-1"
          data-field-path={`outputs[${index}]`}
        >
          <div className="flex-1">
            <TextField
              label={<I18nText text="Output name" />}
              value={outputName(item)}
              readOnly={readOnly}
              issues={issuesUnder(fieldIssues, `outputs[${index}]`)}
              onCommit={(text) => {
                if (text.trim()) {
                  set(['outputs', index, 'name'], text.trim());
                }
              }}
            />
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={ts('Remove output {name}', { name: outputName(item) })}
            disabled={readOnly}
            onClick={() =>
              set(
                items.length === 1 ? ['outputs'] : ['outputs', index],
                undefined
              )
            }
          >
            <X />
          </Button>
        </div>
      ))}
      {!readOnly && (
        <div className="flex items-center gap-1">
          <Input
            className="h-7 flex-1 px-2 text-xs"
            aria-label={ts('New output name')}
            placeholder={ts('Output name')}
            value={name}
            onChange={(event) => setName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
                add();
              }
            }}
          />
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={ts('Add output')}
            disabled={!name.trim()}
            onClick={add}
          >
            <Plus />
          </Button>
        </div>
      )}
      <FieldIssues issues={fieldIssues.outputs} />
    </div>
  );
}
