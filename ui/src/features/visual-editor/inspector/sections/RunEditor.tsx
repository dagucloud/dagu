// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useState, type ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import { FieldIssues } from '../FieldIssues';
import { MonacoField } from '../MonacoField';
import { TextField } from '../TextField';
import type { SectionProps } from '../types';

// The look of the text inputs of the form: one height, radius and inset.
const SELECT_CLASS =
  'h-7 w-full min-w-0 rounded-md border border-input bg-card px-2 text-xs font-normal text-foreground shadow-sm transition-colors focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/20 disabled:cursor-not-allowed disabled:bg-muted disabled:opacity-50';

const SHELLS = ['bash', 'sh', 'zsh', 'pwsh', 'python3'];

// The classic keys of a command, in the order that picks the edited one.
const CLASSIC_KEYS = ['command', 'script'] as const;

function ShellSelect({
  step,
  readOnly,
  onEdit,
  fieldIssues,
  classic,
}: SectionProps & { classic: boolean }) {
  const { ts } = useI18n();
  // A `run:` step takes its shell in `with` (normalizeRunStep in
  // internal/spec/step_v2.go); a classic step keeps the top-level key. The
  // server reports both as `shell`.
  const path = classic ? ['shell'] : ['with', 'shell'];
  const withValue = step.value.with;
  const shell = classic
    ? step.value.shell
    : typeof withValue === 'object' && withValue !== null
      ? (withValue as Record<string, unknown>).shell
      : undefined;
  if (shell !== undefined && typeof shell !== 'string') {
    return (
      <p className="text-xs text-muted-foreground" data-field-path="shell">
        <I18nText text="Shell" />:{' '}
        <span className="font-mono">{JSON.stringify(shell)}</span>
      </p>
    );
  }
  const options =
    shell && !SHELLS.includes(shell) ? [...SHELLS, shell] : SHELLS;
  return (
    <div className="space-y-0.5" data-field-path="shell">
      <label className="block space-y-0.5 text-xs font-medium text-muted-foreground">
        <I18nText text="Shell" />
        <select
          className={SELECT_CLASS}
          value={shell ?? ''}
          disabled={readOnly}
          onChange={(event) =>
            onEdit({
              kind: 'setStepField',
              step: step.key,
              path,
              value: event.target.value || undefined,
            })
          }
        >
          <option value="">{ts('(default)')}</option>
          {options.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>
      </label>
      <FieldIssues issues={fieldIssues.shell} />
    </div>
  );
}

/**
 * Editor of the `run:` command of a step. A step written with the classic
 * `command:` or `script:` key, and no `run:`, shows and writes that key. A
 * one-line command uses a text input; a script with line breaks, or the
 * "Multi-line" toggle, uses a small Monaco editor. Both send one
 * `setStepField` of the key on blur. A list of commands is shown read-only.
 * The shell select sets or removes `with.shell`, or `shell` for a classic
 * step.
 */
export function RunEditor(props: SectionProps): ReactElement {
  const { step, readOnly, onEdit, fieldIssues } = props;
  const { ts } = useI18n();
  const classic =
    'run' in step.value
      ? undefined
      : CLASSIC_KEYS.find((name) => name in step.value);
  const key = classic ?? 'run';
  const run = step.value[key];
  const text = typeof run === 'string' ? run : '';
  const [multiline, setMultiline] = useState(text.includes('\n'));
  const commit = (next: string) =>
    onEdit({
      kind: 'setStepField',
      step: step.key,
      path: [key],
      value: next,
    });

  if (Array.isArray(run)) {
    return (
      <div className="space-y-1" data-field-path={key}>
        <ul className="space-y-0.5 font-mono text-xs">
          {run.map((command, index) => (
            <li key={index} className="whitespace-pre-wrap break-words">
              {String(command)}
            </li>
          ))}
        </ul>
        <p className="text-xs text-muted-foreground">
          <I18nText text="Edit this field in YAML" />
        </p>
        <FieldIssues issues={fieldIssues[key]} />
      </div>
    );
  }

  const lockedMultiline = text.includes('\n');
  return (
    <div className="space-y-1">
      {/* The toggle sits on the row of the label of the field. */}
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-medium text-muted-foreground">
          {multiline || lockedMultiline ? (
            <I18nText text="Script" />
          ) : (
            <I18nText text="Command" />
          )}
        </span>
        <button
          type="button"
          aria-pressed={multiline}
          disabled={readOnly || lockedMultiline}
          onClick={() => setMultiline((value) => !value)}
          className={cn(
            'h-6 rounded px-2 text-xs',
            multiline ? 'bg-muted text-foreground' : 'text-muted-foreground'
          )}
        >
          <I18nText text="Multi-line" />
        </button>
      </div>
      {multiline || lockedMultiline ? (
        <div className="space-y-0.5" data-field-path={key}>
          <MonacoField
            value={text}
            language="shell"
            lines={8}
            readOnly={readOnly}
            ariaLabel={ts('Script')}
            onCommit={commit}
          />
          <FieldIssues issues={fieldIssues[key]} />
        </div>
      ) : (
        <div data-field-path={key}>
          <TextField
            label={<span className="sr-only">{ts('Command')}</span>}
            ariaLabel={ts('Command')}
            value={text}
            readOnly={readOnly}
            issues={fieldIssues[key]}
            onCommit={commit}
          />
        </div>
      )}
      <ShellSelect {...props} classic={classic !== undefined} />
    </div>
  );
}
