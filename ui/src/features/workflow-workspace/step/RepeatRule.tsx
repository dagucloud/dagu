// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  repeatSummary,
  type RepeatPolicyView,
} from '@/components/ui/repeat-summary';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

/**
 * The repeat rule of a step as one line of text, such as
 * `repeat while exit 0 · every 10s · no limit`. The facts line and the step
 * facts render this one component. The text is cut with an ellipsis on one
 * line, or wraps with `wrap`; its tooltip is the whole text. The run data has
 * no finish time between two runs of a repeating step, so the time of the
 * next run is not shown.
 */
export function RepeatRule({
  policy,
  wrap = false,
}: {
  policy: RepeatPolicyView;
  wrap?: boolean;
}) {
  const { ts } = useI18n();
  const summary = repeatSummary(policy, ts);
  return (
    <span
      data-testid="ww-repeat-summary"
      title={summary}
      className={cn('min-w-0', wrap ? 'break-words' : 'truncate')}
    >
      {summary}
    </span>
  );
}
