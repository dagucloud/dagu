// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import dayjs from '@/lib/dayjs';
import { useI18n } from '@/i18n/I18nProvider';

type Props = {
  /** Timestamp string parsable by dayjs; '-' and empty are treated as absent. */
  timestamp?: string | null;
  /** Rendered when the timestamp is absent or unparsable. */
  fallback?: string;
  /** Single-unit form ("5s", "3m", "2h", "4d") instead of "3 minutes ago". */
  compact?: boolean;
  /** Tooltip text; defaults to the parsed timestamp formatted as an absolute time. */
  absolute?: string | null;
  className?: string;
};

function formatCompact(diffSeconds: number, now: string): string {
  const seconds = Math.abs(diffSeconds);
  if (seconds < 5) return now;
  if (seconds < 60) return `${Math.floor(seconds)}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}

/**
 * Renders a timestamp as a live-updating relative time ("3 minutes ago") with
 * the absolute time available in a tooltip.
 */
export function RelativeTime({
  timestamp,
  fallback = '-',
  compact = false,
  absolute,
  className,
}: Props) {
  const { locale, t } = useI18n();
  const parsed = timestamp && timestamp !== '-' ? dayjs(timestamp) : null;
  const isValid = !!parsed && parsed.isValid();
  const [, setTick] = React.useState(0);

  React.useEffect(() => {
    if (!isValid) return;
    const interval = setInterval(
      () => setTick((tick) => tick + 1),
      compact ? 1000 : 15000
    );
    return () => clearInterval(interval);
  }, [isValid, compact]);

  if (!parsed || !isValid) {
    return <span className={className}>{fallback}</span>;
  }

  const label = compact
    ? formatCompact(dayjs().diff(parsed, 'second'), t('time.now'))
    : parsed.locale(locale === 'zh-CN' ? 'zh-cn' : locale).fromNow();

  return (
    <span
      className={className}
      title={absolute ?? parsed.format('YYYY-MM-DD HH:mm:ss Z')}
    >
      {label}
    </span>
  );
}

export default RelativeTime;
