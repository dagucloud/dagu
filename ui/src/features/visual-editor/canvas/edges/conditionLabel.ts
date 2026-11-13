// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { useI18n } from '@/i18n/I18nProvider';
import type { EdgeCondition } from '../../contracts';

type Translate = ReturnType<typeof useI18n>['ts'];

/** The short label on a conditional link: "if verify_1 failed". */
export function conditionLabel(
  ts: Translate,
  condition: EdgeCondition
): string {
  const { step, value } = condition;
  switch (condition.when) {
    case 'failed':
      return ts('if {step} failed', { step });
    case 'succeeded':
      return ts('if {step} succeeded', { step });
    case 'exit':
      return ts(
        condition.negate
          ? 'if {step} exit code is not {code}'
          : 'if {step} exit code is {code}',
        { step, code: value ?? '' }
      );
    default:
      return ts('if {step}: {test}', { step, test: value ?? '' });
  }
}

/** The tooltip of a conditional link: the label and the precondition as written. */
export function conditionTitle(
  ts: Translate,
  condition: EdgeCondition
): string {
  return [
    conditionLabel(ts, condition),
    ts('Precondition: {text}', { text: condition.text }),
  ].join('\n');
}
