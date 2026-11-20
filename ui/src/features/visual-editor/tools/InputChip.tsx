// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Lock } from 'lucide-react';
import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { INPUT_STATE_CHIP, type InputRow } from './dockModel';

/**
 * One input of a DAG tool as a small chip: the name, a star when the DAG
 * requires it, and a lock when the entry fixes it. The colour tells who
 * decides (the model, the entry, or nobody yet); the tooltip has the type, the
 * state, the fixed value and the description of the DAG.
 */
export function InputChip({ input }: { input: InputRow }): ReactElement {
  const { ts } = useI18n();
  const stateText =
    input.state === 'fixed'
      ? `${ts('Fixed')}: ${input.value === '' ? '""' : input.value}`
      : input.state === 'from_ai'
        ? ts('From the model')
        : ts('Open to the model');
  const title = [
    input.type ? `${input.name} (${input.type})` : input.name,
    stateText,
    input.undeclared ? ts('Not declared by the DAG') : '',
    input.description,
  ]
    .filter(Boolean)
    .join('\n');
  return (
    <span
      data-testid="ve-dag-input"
      data-state={input.state}
      title={title}
      className={cn(
        'inline-flex min-w-0 max-w-[6.5rem] items-center gap-0.5 rounded px-1 font-mono text-[10px] leading-4',
        INPUT_STATE_CHIP[input.state],
        input.undeclared && 'text-warning'
      )}
    >
      {input.state === 'fixed' && (
        <Lock className="h-2.5 w-2.5 shrink-0" aria-hidden="true" />
      )}
      <span className="truncate">{input.name}</span>
      {input.required && <span aria-hidden="true">*</span>}
    </span>
  );
}
