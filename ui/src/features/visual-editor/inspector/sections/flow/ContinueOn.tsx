// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { Switch } from '@/components/ui/switch';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { FieldIssues } from '../../FieldIssues';
import { issuesUnder } from '../../issues';
import type { SectionProps } from '../../types';

const SWITCHES = [
  ['failure', 'Continue on failure'],
  ['skipped', 'Continue when skipped'],
  ['mark_success', 'Mark as success'],
] as const;

/** The server also reads this key as `failure`. */
const FAILURE_ALIAS = 'failed';

/** Object form of `continue_on`; the shorthand `failed`/`skipped` becomes its object. */
export function continueOnObject(value: unknown): Record<string, unknown> {
  // The server reads the shorthand in any case and without outer spaces.
  const shorthand =
    typeof value === 'string' ? value.trim().toLowerCase() : undefined;
  if (shorthand === 'failed') {
    return { failure: true };
  }
  if (shorthand === 'skipped') {
    return { skipped: true };
  }
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

/**
 * Switches of `continue_on`. On the object form each switch sets or
 * removes its key. On the string shorthand a change replaces the value
 * with the equivalent object. The last key that goes off stays as `false`:
 * a step without `continue_on` takes `defaults.continue_on`.
 */
export function ContinueOn({
  step,
  readOnly,
  onEdit,
  fieldIssues,
}: SectionProps): ReactElement {
  const { ts } = useI18n();
  const value = step.value.continue_on;
  const object = continueOnObject(value);
  const isOn = (key: string) =>
    object[key] === true ||
    (key === 'failure' && object[FAILURE_ALIAS] === true);
  const setKey = (key: string, next: boolean | undefined) =>
    onEdit({
      kind: 'setStepField',
      step: step.key,
      path: ['continue_on', key],
      value: next,
    });
  const toggle = (key: string, checked: boolean) => {
    // Only the switch of the shorthand can go off.
    if (typeof value === 'string') {
      onEdit({
        kind: 'setStepField',
        step: step.key,
        path: ['continue_on'],
        value: checked ? { ...object, [key]: true } : { [key]: false },
      });
      return;
    }
    const others = Object.keys(object).filter(
      (k) => k !== key && !(key === 'failure' && k === FAILURE_ALIAS)
    );
    if (checked) setKey(key, true);
    else if (others.length === 0) setKey(key, false);
    else if (key in object) setKey(key, undefined);
    // Drop the alias so that one key decides the failure switch.
    if (key === 'failure' && FAILURE_ALIAS in object) {
      setKey(FAILURE_ALIAS, undefined);
    }
  };
  return (
    <div className="space-y-1" data-field-path="continue_on">
      {SWITCHES.map(([key, label]) => (
        <label
          key={key}
          className="flex items-center justify-between gap-2 text-xs"
        >
          <I18nText text={label} />
          <Switch
            aria-label={ts(label)}
            checked={isOn(key)}
            disabled={readOnly}
            onCheckedChange={(checked) => toggle(key, checked)}
          />
        </label>
      ))}
      <FieldIssues issues={issuesUnder(fieldIssues, 'continue_on')} />
    </div>
  );
}
