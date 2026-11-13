// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus, X } from 'lucide-react';
import { useState, type ReactElement, type ReactNode } from 'react';
import { IconButton } from '@/components/ui/icon-button';
import { Input } from '@/components/ui/input';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

/**
 * A list of strings with a row for each item and a row that adds one. A
 * change sends the whole list.
 */
export function StringList({
  label,
  values,
  readOnly,
  onChange,
}: {
  label: ReactNode;
  values: string[];
  readOnly: boolean;
  onChange(next: string[]): void;
}): ReactElement {
  const { ts } = useI18n();
  const [draft, setDraft] = useState('');
  const add = () => {
    const text = draft.trim();
    if (text === '' || values.includes(text)) return;
    onChange([...values, text]);
    setDraft('');
  };
  return (
    <div className="space-y-0.5">
      <div className="text-xs font-medium text-muted-foreground">{label}</div>
      {values.map((value, index) => (
        <div key={`${value}:${index}`} className="flex items-center gap-1">
          <span className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-0.5 text-xs">
            {value}
          </span>
          {!readOnly && (
            <IconButton
              size="sm"
              label={ts('Remove {item}', { item: value })}
              icon={X}
              onClick={() => onChange(values.filter((_, i) => i !== index))}
            />
          )}
        </div>
      ))}
      {!readOnly && (
        <div className="flex items-center gap-1">
          <Input
            className="h-7 flex-1 px-2 text-xs"
            aria-label={ts('New item')}
            placeholder={ts('Add an item')}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                add();
              }
            }}
          />
          <IconButton
            size="sm"
            label={ts('Add item')}
            icon={Plus}
            disabled={draft.trim() === ''}
            onClick={add}
          />
        </div>
      )}
    </div>
  );
}

/**
 * A choice between a few modes of a value, as a row of buttons in one frame,
 * like the "Value | Reference" choice of the parameters form.
 */
export function ModeToggle<T extends string>({
  label,
  value,
  options,
  readOnly,
  onChange,
}: {
  label: string;
  value: T;
  options: { value: T; label: string }[];
  readOnly: boolean;
  onChange(value: T): void;
}): ReactElement {
  return (
    <div
      role="group"
      aria-label={label}
      className="flex shrink-0 overflow-hidden rounded border border-border"
    >
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={value === option.value}
          disabled={readOnly}
          onClick={() => onChange(option.value)}
          className={cn(
            'h-6 px-2 text-xs disabled:opacity-50',
            value === option.value
              ? 'bg-muted font-medium text-foreground'
              : 'text-muted-foreground'
          )}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
