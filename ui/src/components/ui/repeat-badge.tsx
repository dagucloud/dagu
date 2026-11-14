// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Repeat } from 'lucide-react';
import type { ReactElement } from 'react';
import { Badge } from '@/components/ui/badge';
import {
  repeatInterval,
  repeatIteration,
  repeatSummary,
  type RepeatPolicyView,
} from '@/components/ui/repeat-summary';
import { useI18n } from '@/i18n/I18nProvider';
import { formatCompactCount, formatExactCount } from '@/lib/formatCompactCount';
import { cn } from '@/lib/utils';

type Props = {
  /** The repeat policy of the step; see readRepeatPolicy. */
  policy: RepeatPolicyView;
  /** How many times the step has run in the run that the host shows. */
  count?: number;
  /** The step runs now: the badge names the running iteration. */
  running?: boolean;
  /** Icon and count only, for a card that has no room for text. */
  compact?: boolean;
  className?: string;
};

/**
 * The repeat policy of a step, in a small info badge: the repeat icon with
 * `×3` once the step has run (`×1.2k` from 1 000, see formatCompactCount; the
 * title gives the whole count), else with the interval (`every 10s`) or the
 * mode. The tooltip is the whole summary and the iteration. Every surface
 * that shows a repeat policy renders this one badge.
 */
export function RepeatBadge({
  policy,
  count = 0,
  running = false,
  compact = false,
  className,
}: Props): ReactElement {
  const { ts, locale } = useI18n();
  const summary = repeatSummary(policy, ts);
  const iteration = repeatIteration(policy, count, running, ts, (n) =>
    formatExactCount(n, locale)
  );
  const title = iteration ? `${summary}\n${iteration}` : summary;
  const countText = `×${formatCompactCount(count)}`;
  const label =
    count > 0
      ? countText
      : policy.intervalSec !== undefined
        ? ts('every {interval}', {
            interval: repeatInterval(policy.intervalSec),
          })
        : ts(policy.mode);
  return (
    <Badge
      variant="info"
      size="sm"
      role="img"
      title={title}
      aria-label={title}
      data-testid="repeat-badge"
      data-running={running ? 'true' : undefined}
      className={cn('tabular-nums', className)}
    >
      <Repeat className="size-3" aria-hidden="true" />
      {compact ? (count > 0 ? countText : null) : label}
    </Badge>
  );
}

export default RepeatBadge;
