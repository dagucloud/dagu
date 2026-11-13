// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus } from 'lucide-react';
import {
  useEffect,
  useId,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react';
import { Button } from '@/components/ui/button';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import type {
  DagEditOp,
  ReferenceCandidate,
  StepKey,
  ValidationIssue,
} from '../../../contracts';
import { ReferenceInput } from '../../../references';
import { FieldIssues } from '../../FieldIssues';
import { TextField } from '../../TextField';
import { numberText, parseNumberText, type NumberRule } from './model';

/** A function that sets one field of the step, or removes it when `value` is undefined. */
export type SetField = (path: (string | number)[], value: unknown) => void;

export function makeSetField(
  step: StepKey,
  onEdit: (op: DagEditOp) => void
): SetField {
  return (path, value) => onEdit({ kind: 'setStepField', step, path, value });
}

/** A client-side error shown under a field. */
export function errorIssue(message: string): ValidationIssue {
  return {
    severity: ValidationIssueSeverity.error,
    source: ValidationIssueSource.build,
    message,
  };
}

/**
 * A field of the chat form: the label above, the control, a short grey help
 * line and the issues under it. `path` names the field for a host that
 * focuses it; `empty` and `required` tell whether it needs a value.
 */
export function ChatField({
  label,
  htmlFor,
  help,
  issues,
  path,
  required,
  empty,
  children,
}: {
  label: ReactNode;
  htmlFor?: string;
  help?: ReactNode;
  issues?: ValidationIssue[];
  path?: string;
  required?: boolean;
  empty?: boolean;
  children: ReactNode;
}): ReactElement {
  return (
    <div
      className="space-y-0.5"
      data-field-path={path}
      data-required={required ? 'true' : undefined}
      data-empty={empty ? 'true' : undefined}
    >
      <label
        htmlFor={htmlFor}
        className="block text-xs font-medium text-muted-foreground"
      >
        {label}
      </label>
      {children}
      {help && <p className="text-[11px] text-muted-foreground">{help}</p>}
      <FieldIssues issues={issues} />
    </div>
  );
}

/**
 * Text of a field that takes `${...}` references, in one or several lines.
 * It keeps a draft and sends it on blur, and when the field goes away, only
 * when the text differs from `value`. `commit` receives undefined for an
 * empty text, so that the key goes.
 */
export function ReferenceField({
  label,
  help,
  value,
  candidates,
  readOnly,
  multiline = false,
  rows,
  resizable,
  placeholder,
  issues,
  path,
  required,
  labelHidden = false,
  highlight = false,
  action,
  onCommit,
}: {
  label: string;
  help?: ReactNode;
  value: string;
  candidates: ReferenceCandidate[];
  readOnly: boolean;
  multiline?: boolean;
  rows?: number;
  resizable?: boolean;
  placeholder?: string;
  issues?: ValidationIssue[];
  path?: string;
  required?: boolean;
  /** The label stays the accessible name, but is not drawn (a row of a list). */
  labelHidden?: boolean;
  /** A text of free words (a prompt): references and Markdown marks show in colour. */
  highlight?: boolean;
  /** A control beside the field. */
  action?: ReactNode;
  onCommit(text: string | undefined): void;
}): ReactElement {
  const id = useId();
  const [draft, setDraft] = useState(value);
  const shown = useRef(value);
  const draftRef = useRef(value);
  const valueRef = useRef(value);
  valueRef.current = value;
  const commitRef = useRef(onCommit);
  commitRef.current = onCommit;
  const lockedRef = useRef(readOnly);
  lockedRef.current = readOnly;

  useEffect(() => {
    if (value !== shown.current) {
      shown.current = value;
      draftRef.current = value;
      setDraft(value);
    }
  }, [value]);

  const commit = () => {
    if (lockedRef.current || draftRef.current === valueRef.current) return;
    commitRef.current(draftRef.current === '' ? undefined : draftRef.current);
  };
  // A step that is selected away from, with text not yet sent.
  const commitNow = useRef(commit);
  commitNow.current = commit;
  useEffect(() => () => commitNow.current(), []);

  return (
    <ChatField
      label={labelHidden ? <span className="sr-only">{label}</span> : label}
      htmlFor={id}
      help={help}
      issues={issues}
      path={path}
      required={required}
      empty={value === ''}
    >
      <div className="flex items-start gap-1">
        <div className="min-w-0 flex-1">
          <ReferenceInput
            id={id}
            aria-label={label}
            value={draft}
            candidates={candidates}
            disabled={readOnly}
            multiline={multiline}
            rows={rows}
            resizable={resizable}
            highlight={highlight}
            placeholder={placeholder}
            onChange={(text) => {
              draftRef.current = text;
              setDraft(text);
            }}
            onBlur={commit}
          />
        </div>
        {action}
      </div>
    </ChatField>
  );
}

/**
 * A number field. The text is checked when it is sent: a text that is not
 * a number in the range shows an error and sends nothing. Empty text
 * removes the key.
 */
export function NumberField({
  label,
  help,
  value,
  rule,
  fallback,
  readOnly,
  issues,
  path,
  invalid,
  onSet,
}: {
  label: string;
  help?: ReactNode;
  value: unknown;
  rule: NumberRule;
  /** The default that the placeholder shows, when the step has one. */
  fallback?: number;
  readOnly: boolean;
  issues?: ValidationIssue[];
  path: string;
  /** The message for a text that does not fit the rule. */
  invalid: string;
  onSet(value: number | string | undefined): void;
}): ReactElement {
  const { ts } = useI18n();
  const [bad, setBad] = useState(false);
  return (
    <div data-field-path={path}>
      <TextField
        label={label}
        help={help}
        value={numberText(value)}
        placeholder={
          fallback === undefined
            ? undefined
            : ts('Default: {count}', { count: fallback })
        }
        readOnly={readOnly}
        issues={bad ? [errorIssue(invalid), ...(issues ?? [])] : issues}
        onCommit={(text) => {
          const parsed = parseNumberText(text, rule);
          setBad(!parsed.ok);
          if (parsed.ok) onSet(parsed.value);
        }}
      />
    </div>
  );
}

/** The button under a list that adds a row; it looks like the one of the Tools section. */
export function AddButton({
  label,
  disabled,
  onClick,
}: {
  label: string;
  disabled: boolean;
  onClick(): void;
}): ReactElement {
  return (
    <Button
      type="button"
      variant="outline"
      className="h-7 gap-1 px-2 text-xs"
      disabled={disabled}
      onClick={onClick}
    >
      <Plus className="size-3.5" aria-hidden />
      {label}
    </Button>
  );
}

/** A value that the form does not edit: the key, the value and a hint to use the YAML. */
export function YamlOnlyValue({
  name,
  value,
}: {
  name: string;
  value: unknown;
}): ReactElement {
  return (
    <p
      className="break-words text-[11px] text-muted-foreground"
      data-testid="ve-yaml-only"
    >
      <span className="font-mono">{name}</span>: {JSON.stringify(value)}{' '}
      <I18nText text="Edit this field in YAML" />
    </p>
  );
}
