// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useId, type ReactElement } from 'react';
import { Switch } from '@/components/ui/switch';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import type { ValidationIssue } from '../../../contracts';
import { FieldIssues } from '../../FieldIssues';
import { issuesUnder } from '../../issues';
import type { SectionProps } from '../../types';
import { makeSetField, NumberField, YamlOnlyValue } from './fields';
import { withOf } from './model';

/** Keys that the Options section only shows: a form for them is heavy. */
const SHOWN_KEYS = ['thinking', 'web_search'] as const;

/** The switch of `with.stream` of a chat step. */
function StreamField({
  value,
  readOnly,
  issues,
  onSet,
}: {
  value: unknown;
  readOnly: boolean;
  issues: ValidationIssue[];
  onSet(value: boolean | undefined): void;
}): ReactElement {
  const { ts } = useI18n();
  const id = useId();
  return (
    <div className="space-y-0.5" data-field-path="with.stream">
      <div className="flex items-center justify-between gap-2">
        <label
          htmlFor={id}
          className="text-xs font-medium text-muted-foreground"
        >
          <I18nText text="Stream the answer" />
        </label>
        <Switch
          id={id}
          checked={value !== false}
          disabled={readOnly}
          // On is the default: the key goes instead of `stream: true`.
          onCheckedChange={(checked) => onSet(checked ? undefined : false)}
        />
      </div>
      <p className="text-[11px] text-muted-foreground">
        {ts('Write the answer to the output while the model produces it.')}
      </p>
      <FieldIssues issues={issues} />
    </div>
  );
}

/**
 * Options of a chat step, in a section that starts closed: streaming, the
 * sampling numbers and the limit of tool rounds. The limits of the tools
 * are in the Tools section. `thinking` and `web_search` show read-only when
 * the step sets them.
 */
export function OptionsSection({
  step,
  readOnly,
  onEdit,
  fieldIssues,
}: SectionProps): ReactElement {
  const { ts } = useI18n();
  const set = makeSetField(step.key, onEdit);
  const withMap = withOf(step.value);
  const issues = (key: string) => issuesUnder(fieldIssues, `with.${key}`);
  const shown = SHOWN_KEYS.filter((key) => withMap[key] !== undefined);
  return (
    <div className="space-y-3">
      <StreamField
        value={withMap.stream}
        readOnly={readOnly}
        issues={issues('stream')}
        onSet={(value) => set(['with', 'stream'], value)}
      />
      <NumberField
        label={ts('Temperature')}
        help={ts('From 0 to 2. A low value gives steadier answers.')}
        value={withMap.temperature}
        rule={{ integer: false, min: 0, max: 2 }}
        readOnly={readOnly}
        issues={issues('temperature')}
        path="with.temperature"
        invalid={ts('Enter a number from 0 to 2')}
        onSet={(value) => set(['with', 'temperature'], value)}
      />
      <NumberField
        label={ts('Maximum number of tokens')}
        help={ts('The longest answer that the model can write.')}
        value={withMap.max_tokens}
        rule={{ integer: true, min: 1 }}
        readOnly={readOnly}
        issues={issues('max_tokens')}
        path="with.max_tokens"
        invalid={ts('Enter a whole number, 1 or more')}
        onSet={(value) => set(['with', 'max_tokens'], value)}
      />
      <NumberField
        label={ts('Top P')}
        help={ts(
          'From 0 to 1. Another way to limit how varied the answers are.'
        )}
        value={withMap.top_p}
        rule={{ integer: false, min: 0, max: 1 }}
        readOnly={readOnly}
        issues={issues('top_p')}
        path="with.top_p"
        invalid={ts('Enter a number from 0 to 1')}
        onSet={(value) => set(['with', 'top_p'], value)}
      />
      <NumberField
        label={ts('Maximum number of tool rounds')}
        help={ts(
          'How many times the model can call tools before it must answer.'
        )}
        value={withMap.max_tool_iterations}
        rule={{ integer: true, min: 1 }}
        fallback={10}
        readOnly={readOnly}
        issues={issues('max_tool_iterations')}
        path="with.max_tool_iterations"
        invalid={ts('Enter a whole number, 1 or more')}
        onSet={(value) => set(['with', 'max_tool_iterations'], value)}
      />
      {shown.map((key) => (
        <div key={key} data-field-path={`with.${key}`}>
          <YamlOnlyValue name={key} value={withMap[key]} />
          <FieldIssues issues={issues(key)} />
        </div>
      ))}
    </div>
  );
}
