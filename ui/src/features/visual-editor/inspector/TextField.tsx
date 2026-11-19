// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useEffect,
  useId,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
} from 'react';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import { ReferenceInput } from '@/features/visual-editor/references';
import type { ReferenceCandidate, ValidationIssue } from '../contracts';
import { FieldIssues } from './FieldIssues';

export interface TextFieldProps {
  label: ReactNode;
  /** Accessible name when `label` is not plain text. */
  ariaLabel?: string;
  value: string;
  readOnly: boolean;
  multiline?: boolean;
  placeholder?: string;
  issues?: ValidationIssue[];
  /** References to offer. Without them, the field takes plain text only. */
  candidates?: ReferenceCandidate[];
  /**
   * Returns why a draft cannot be sent. The field then keeps the draft and
   * shows the reason until the draft is valid or restored.
   */
  validate?(text: string): string | undefined;
  /** Called on blur (and Enter for one line) when the text changed. */
  onCommit(text: string): void;
}

/**
 * Labeled text input with a local draft. The draft is sent on blur, or on
 * Enter for a one-line input, only when it differs from `value`. Escape
 * restores `value`. The draft resets when `value` changes from outside. An
 * open list of references takes Enter and Escape first.
 */
export function TextField({
  label,
  ariaLabel,
  value,
  readOnly,
  multiline = false,
  placeholder,
  issues,
  candidates,
  validate,
  onCommit,
}: TextFieldProps): ReactElement {
  const id = useId();
  const [draft, setDraft] = useState(value);
  // Set when a draft was refused; the reason then follows the draft.
  const [refused, setRefused] = useState(false);
  const shown = useRef(value);
  useEffect(() => {
    if (value !== shown.current) {
      shown.current = value;
      setDraft(value);
      setRefused(false);
    }
  }, [value]);

  const commit = () => {
    if (readOnly || draft === value) return;
    if (validate?.(draft) !== undefined) {
      setRefused(true);
      return;
    }
    setRefused(false);
    onCommit(draft);
  };
  const reason = refused ? validate?.(draft) : undefined;
  const fieldIssues: ValidationIssue[] | undefined =
    reason === undefined
      ? issues
      : [
          ...(issues ?? []),
          {
            severity: ValidationIssueSeverity.error,
            source: ValidationIssueSource.build,
            message: reason,
          },
        ];
  const common = {
    id,
    value: draft,
    placeholder,
    disabled: readOnly,
    'aria-label': ariaLabel,
    'aria-invalid':
      fieldIssues?.some((issue) => issue.severity === 'error') || undefined,
    onBlur: commit,
    onKeyDown: (event: KeyboardEvent) => {
      if (event.nativeEvent.isComposing) return;
      if (event.key === 'Enter' && !multiline) {
        commit();
      } else if (event.key === 'Escape') {
        setDraft(value);
        setRefused(false);
      }
    },
  };

  return (
    <div className="space-y-0.5">
      <label
        htmlFor={id}
        className="block text-xs font-medium text-muted-foreground"
      >
        {label}
      </label>
      {candidates ? (
        <ReferenceInput
          {...common}
          candidates={candidates}
          multiline={multiline}
          onChange={setDraft}
        />
      ) : multiline ? (
        <Textarea
          {...common}
          rows={2}
          className="min-h-0 px-2 py-1 text-xs"
          onChange={(event) => setDraft(event.target.value)}
        />
      ) : (
        <Input
          {...common}
          className="h-7 px-2 text-xs"
          onChange={(event) => setDraft(event.target.value)}
        />
      )}
      <FieldIssues issues={fieldIssues} />
    </div>
  );
}
