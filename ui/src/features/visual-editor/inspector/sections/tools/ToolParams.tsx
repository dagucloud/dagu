// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus, X } from 'lucide-react';
import { useState, type ReactElement } from 'react';
import { IconButton } from '@/components/ui/icon-button';
import { Input } from '@/components/ui/input';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import type { ReferenceCandidate, ValidationIssue } from '../../../contracts';
import type { ToolParamModel } from '../../../tools/model';
import type { ToolInputHint } from '../../../tools/targets';
import { FieldIssues } from '../../FieldIssues';
import { issuesUnder } from '../../issues';
import { TextField } from '../../TextField';
import { ReferenceField } from '../chat/fields';
import { ModeToggle } from './fields';

/** Changes one key of the entry: `path` is relative to it, undefined removes. */
export type ChangeEntry = (path: (string | number)[], value: unknown) => void;

/** The text of a fixed value: a string as it is, anything else as JSON. */
export function fixedText(value: unknown): string {
  if (value === undefined) return '';
  return typeof value === 'string' ? value : JSON.stringify(value);
}

/**
 * The value that a fixed-value box gives back. A value that was not a
 * string stays the type that it had when the new text is valid JSON of that
 * type (a number stays a number); else the text is a string.
 */
export function parseFixed(text: string, before: unknown): unknown {
  if (typeof before === 'string' || before === undefined) return text;
  try {
    const parsed: unknown = JSON.parse(text);
    return typeof parsed === typeof before ? parsed : text;
  } catch {
    return text;
  }
}

/** The YAML value for an input that the model decides. */
function fromAi(description: string): unknown {
  return description.trim() === ''
    ? { from_ai: true }
    : { from_ai: { description: description.trim() } };
}

function ParamRow({
  param,
  hint,
  path,
  readOnly,
  candidates,
  issues,
  onChange,
}: {
  param: ToolParamModel;
  hint: ToolInputHint | undefined;
  /** The field path of the inputs of the entry: `with.dags[0].params`. */
  path: string;
  readOnly: boolean;
  candidates: ReferenceCandidate[];
  issues: ValidationIssue[];
  onChange: ChangeEntry;
}): ReactElement {
  const { ts } = useI18n();
  const key = ['params', param.name];
  const state = param.state;
  return (
    <div
      className="space-y-1"
      data-testid="ve-tool-param"
      data-state={state}
      data-field-path={`${path}.${param.name}`}
    >
      <div className="flex items-center gap-1">
        <span className="min-w-0 flex-1 truncate font-mono text-xs font-medium">
          {param.name}
        </span>
        <ModeToggle
          label={ts('Who decides {name}', { name: param.name })}
          value={state}
          readOnly={readOnly}
          options={[
            { value: 'fixed', label: ts('Fixed value') },
            { value: 'from_ai', label: ts('Model decides') },
          ]}
          onChange={(next) => {
            if (next !== state) {
              onChange(key, next === 'fixed' ? '' : fromAi(''));
            }
          }}
        />
        {!readOnly && (
          <IconButton
            size="sm"
            label={ts('Remove input {name}', { name: param.name })}
            icon={X}
            onClick={() => onChange(key, undefined)}
          />
        )}
      </div>
      {hint?.description && (
        <p className="text-[11px] text-muted-foreground">{hint.description}</p>
      )}
      {state === 'fixed' ? (
        <ReferenceField
          label={ts('Value of {name}', { name: param.name })}
          labelHidden
          value={fixedText(param.value)}
          candidates={candidates}
          readOnly={readOnly}
          onCommit={(text) =>
            onChange(key, parseFixed(text ?? '', param.value))
          }
        />
      ) : (
        <TextField
          label={<I18nText text="Description for the model" />}
          ariaLabel={ts('Description of {name} for the model', {
            name: param.name,
          })}
          value={param.description ?? ''}
          readOnly={readOnly}
          onCommit={(text) => onChange(key, fromAi(text))}
        />
      )}
      <FieldIssues issues={issues} />
    </div>
  );
}

/**
 * The inputs of a DAG tool. An input that is not listed stays
 * open for the model. Each listed input is either a fixed value, which the
 * model never sees, or an input that the model decides, with an optional text
 * for it. `known` are the inputs that the tool declares: they are listed as
 * inputs that the model decides, and a change writes them. `path` is the
 * field path of the inputs, for the issues and for a host that focuses them.
 */
export function ToolParams({
  params,
  known,
  path,
  fieldIssues,
  candidates,
  readOnly,
  onChange,
}: {
  params: ToolParamModel[];
  known: ToolInputHint[];
  path: string;
  fieldIssues: Record<string, ValidationIssue[]>;
  candidates: ReferenceCandidate[];
  readOnly: boolean;
  onChange: ChangeEntry;
}): ReactElement {
  const { ts } = useI18n();
  const [name, setName] = useState('');
  const written = new Set(params.map((param) => param.name));
  const rows: ToolParamModel[] = [
    ...params,
    ...known
      .filter((input) => !written.has(input.name))
      .map((input): ToolParamModel => ({ name: input.name, state: 'from_ai' })),
  ];
  const add = () => {
    const input = name.trim();
    if (input === '' || rows.some((row) => row.name === input)) return;
    onChange(['params', input], fromAi(''));
    setName('');
  };
  return (
    <div
      className="space-y-2"
      data-testid="ve-tool-params"
      data-field-path={path}
    >
      {rows.length === 0 && (
        <p className="text-xs text-muted-foreground">
          <I18nText text="The model decides every input of this tool." />
        </p>
      )}
      {rows.map((param) => (
        <ParamRow
          key={param.name}
          param={param}
          hint={known.find((input) => input.name === param.name)}
          path={path}
          readOnly={readOnly}
          candidates={candidates}
          issues={issuesUnder(fieldIssues, `${path}.${param.name}`)}
          onChange={onChange}
        />
      ))}
      {!readOnly && (
        <div className="flex items-center gap-1">
          <Input
            className="h-7 flex-1 px-2 text-xs"
            aria-label={ts('New input name')}
            placeholder={ts('Input name')}
            value={name}
            onChange={(event) => setName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                add();
              }
            }}
          />
          <IconButton
            size="sm"
            label={ts('Add input')}
            icon={Plus}
            disabled={name.trim() === ''}
            onClick={add}
          />
        </div>
      )}
      <FieldIssues issues={fieldIssues[path]} />
    </div>
  );
}
