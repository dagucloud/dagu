// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { FieldIssues } from '../../FieldIssues';
import { issuesUnder } from '../../issues';
import type { SectionProps } from '../../types';

/** Read-only list of the dependencies of a step; the canvas edits them. */
export function Dependencies({
  step,
  fieldIssues,
}: SectionProps): ReactElement {
  const { ts } = useI18n();
  return (
    <div className="space-y-0.5" data-field-path="depends">
      <div className="text-xs font-medium text-muted-foreground">
        <I18nText text="Depends on" />
      </div>
      {step.depends.length > 0 ? (
        <ul className="flex flex-wrap gap-1" aria-label={ts('Depends on')}>
          {step.depends.map((name) => (
            <li
              key={name}
              className="max-w-full whitespace-normal break-words rounded bg-muted px-1.5 py-0.5 font-mono text-xs"
            >
              {name}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-xs text-muted-foreground">
          <I18nText text="No dependencies" />
        </p>
      )}
      <p className="text-xs text-muted-foreground">
        <I18nText text="Edit dependencies on the canvas" />
      </p>
      <FieldIssues issues={issuesUnder(fieldIssues, 'depends')} />
    </div>
  );
}
