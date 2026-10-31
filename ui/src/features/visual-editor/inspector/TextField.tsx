// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useEffect,
  useId,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import type { ValidationIssue } from '../contracts';
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
  /** Called on blur (and Enter for one line) when the text changed. */
  onCommit(text: string): void;
}

/**
 * Labeled text input with a local draft. The draft is sent on blur, or on
 * Enter for a one-line input, only when it differs from `value`. Escape
 * restores `value`. The draft resets when `value` changes from outside.
 */
export function TextField({
  label,
  ariaLabel,
  value,
  readOnly,
  multiline = false,
  placeholder,
  issues,
  onCommit,
}: TextFieldProps): ReactElement {
  const id = useId();
  const [draft, setDraft] = useState(value);
  const shown = useRef(value);
  useEffect(() => {
    if (value !== shown.current) {
      shown.current = value;
      setDraft(value);
    }
  }, [value]);

  const commit = () => {
    if (!readOnly && draft !== value) {
      onCommit(draft);
    }
  };
  const common = {
    id,
    value: draft,
    placeholder,
    disabled: readOnly,
    'aria-label': ariaLabel,
    'aria-invalid':
      issues?.some((issue) => issue.severity === 'error') || undefined,
    onBlur: commit,
  };

  return (
    <div className="space-y-0.5">
      <label
        htmlFor={id}
        className="block text-xs font-medium text-muted-foreground"
      >
        {label}
      </label>
      {multiline ? (
        <Textarea
          {...common}
          rows={2}
          className="min-h-0 px-2 py-1 text-xs"
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.nativeEvent.isComposing) return;
            if (event.key === 'Escape') {
              setDraft(value);
            }
          }}
        />
      ) : (
        <Input
          {...common}
          className="h-7 px-2 text-xs"
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.nativeEvent.isComposing) return;
            if (event.key === 'Enter') {
              commit();
            } else if (event.key === 'Escape') {
              setDraft(value);
            }
          }}
        />
      )}
      <FieldIssues issues={issues} />
    </div>
  );
}
