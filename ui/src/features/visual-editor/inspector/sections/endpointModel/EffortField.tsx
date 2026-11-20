// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useId, type ReactElement } from 'react';
import { Picker, type PickerItem } from '@/features/ai-endpoints/Picker';
import { useI18n } from '@/i18n/I18nProvider';
import type { ValidationIssue } from '../../../contracts';
import { ChatField } from '../chat/fields';

/**
 * The field `endpoint.effort` of the form of an agent step or of a chat step:
 * Default (which removes the key) and the levels that the model of the step
 * supports.
 */
export function EffortField({
  help,
  value,
  levels,
  readOnly,
  issues,
  onSet,
}: {
  help: string;
  value: string;
  levels: readonly string[];
  readOnly: boolean;
  issues: ValidationIssue[];
  onSet(level: string | undefined): void;
}): ReactElement {
  const { ts } = useI18n();
  const id = useId();
  const items: PickerItem[] = [
    { value: '', label: ts('Default') },
    ...levels.map((level) => ({ value: level, label: level })),
  ];
  if (value !== '' && !levels.includes(value)) {
    items.push({ value, label: value });
  }
  return (
    <ChatField
      label={ts('Effort')}
      htmlFor={id}
      help={help}
      issues={issues}
      path="with.endpoint.effort"
    >
      <Picker
        id={id}
        label={ts('Effort')}
        disabled={readOnly}
        items={items}
        value={value}
        onSelect={(level) => onSet(level || undefined)}
      >
        <span className="truncate">{value === '' ? ts('Default') : value}</span>
      </Picker>
    </ChatField>
  );
}
