// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { useI18n } from '@/i18n/I18nProvider';

export type LogViewMode = 'tail' | 'page';

/** Chooses which part of a log is loaded: its latest lines, or one page. */
export function LogViewModeSwitch({
  value,
  onChange,
  disabled,
}: {
  value: LogViewMode;
  onChange: (mode: LogViewMode) => void;
  disabled?: boolean;
}): React.ReactElement {
  const { ts } = useI18n();
  return (
    <ToggleGroup
      aria-label={ts('Lines shown')}
      className="max-w-full shrink-0 overflow-x-auto p-0.5"
    >
      <ToggleButton
        value="page"
        groupValue={value}
        onClick={() => onChange(value === 'page' ? 'tail' : 'page')}
        disabled={disabled}
        className="h-6 whitespace-nowrap px-2"
      >
        {ts('Page View')}
      </ToggleButton>
    </ToggleGroup>
  );
}
