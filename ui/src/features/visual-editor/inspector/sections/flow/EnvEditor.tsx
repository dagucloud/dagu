// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { FieldIssues } from '../../FieldIssues';
import { issuesUnder } from '../../issues';
import type { SectionProps } from '../../types';
import { KeyValueEditor } from '../../widgets';
import { envOps, readEnv } from './envForm';

/** Environment variables of a step, kept in the form that the step uses. */
export function EnvEditor({
  step,
  readOnly,
  onEdit,
  fieldIssues,
}: SectionProps): ReactElement {
  const { ts } = useI18n();
  const env = readEnv(step.value.env);
  return (
    <div className="space-y-0.5" data-field-path="env">
      <div className="text-xs font-medium text-muted-foreground">
        <I18nText text="Environment" />
      </div>
      {env.form === 'other' ? (
        <p className="text-xs text-muted-foreground">
          <I18nText text="Edit this field in YAML" />
        </p>
      ) : (
        <KeyValueEditor
          label={ts('Environment')}
          value={env.entries}
          readOnly={readOnly}
          onChange={(next) =>
            envOps(step.key, step.value.env, next).forEach((op) => onEdit(op))
          }
        />
      )}
      <FieldIssues issues={issuesUnder(fieldIssues, 'env')} />
    </div>
  );
}
