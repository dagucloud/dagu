// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { issuesUnder } from '../../issues';
import type { SectionProps } from '../../types';
import { makeSetField, NumberField } from '../chat/fields';
import { withOf } from '../chat/model';
import { TIMEOUT_KEY } from './model';

/**
 * Limits of an agent step: the tool iterations of the loop, the calls of the
 * tool bridge and the timeout of the step (`timeout_sec`, a step key).
 * Each field sends its value when it is a number in the range, or a
 * `${...}` reference; empty text removes the key.
 */
export function AgentRunLimits({
  step,
  readOnly,
  onEdit,
  fieldIssues,
}: SectionProps): ReactElement {
  const { ts } = useI18n();
  const set = makeSetField(step.key, onEdit);
  const withMap = withOf(step.value);
  const issues = (key: string) => issuesUnder(fieldIssues, key);
  return (
    <div className="space-y-3">
      <NumberField
        label={ts('Maximum number of tool iterations')}
        help={ts(
          'The most model requests of one attempt. An attempt that reaches the limit before an answer fails.'
        )}
        value={withMap.max_tool_iterations}
        rule={{ integer: true, min: 1 }}
        readOnly={readOnly}
        issues={issues('with.max_tool_iterations')}
        path="with.max_tool_iterations"
        invalid={ts('Enter a whole number, 1 or more')}
        onSet={(value) => set(['with', 'max_tool_iterations'], value)}
      />
      <NumberField
        label={ts('Maximum number of tool calls')}
        help={ts(
          'The most calls of DAG tools in one attempt. A call over the limit gets an error result. The attempt does not fail.'
        )}
        value={withMap.max_tool_calls}
        rule={{ integer: true, min: 1 }}
        fallback={100}
        readOnly={readOnly}
        issues={issues('with.max_tool_calls')}
        path="with.max_tool_calls"
        invalid={ts('Enter a whole number, 1 or more')}
        onSet={(value) => set(['with', 'max_tool_calls'], value)}
      />
      <NumberField
        label={ts('Timeout (seconds)')}
        help={ts('Dagu stops the step at this limit.')}
        value={step.value[TIMEOUT_KEY]}
        rule={{ integer: true, min: 0 }}
        readOnly={readOnly}
        issues={issues(TIMEOUT_KEY)}
        path={TIMEOUT_KEY}
        invalid={ts('Enter a whole number, 0 or more')}
        onSet={(value) => set([TIMEOUT_KEY], value)}
      />
    </div>
  );
}
