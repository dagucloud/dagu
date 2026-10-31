// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import dayjs from '@/lib/dayjs';
import { useClock } from '@/hooks/useClock';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

type Props = {
  /** Timestamp string parsable by dayjs; '-' and empty are treated as absent. */
  timestamp?: string | null;
  /** Rendered when the timestamp is absent or unparsable. */
  fallback?: string;
  /** Single-unit form ("5s", "3m", "2h", "4d") instead of "3 minutes ago". */
  compact?: boolean;
  /**
   * With `compact`, a lapse of 5 s or more reads "3m ago" instead of "3m", and
   * rounds to the nearest unit as the long form does, so that both agree.
   */
  ago?: boolean;
  /**
   * With `compact`, keeps the width of the widest text of the unit that shows
   * now ("99m ago"), so that a text that ticks moves nothing next to it.
   */
  stable?: boolean;
  /** Tooltip text; defaults to the parsed timestamp formatted as an absolute time. */
  absolute?: string | null;
  className?: string;
};

interface CompactUnits {
  s: string;
  m: string;
  h: string;
  d: string;
}

// ja and zh-CN write the unit as a word ("5時間前"), not as a Latin letter.
const EN_UNITS: CompactUnits = { s: 's', m: 'm', h: 'h', d: 'd' };
const COMPACT_UNITS: Record<string, CompactUnits | undefined> = {
  'zh-CN': { s: '秒', m: '分钟', h: '小时', d: '天' },
  ja: { s: '秒', m: '分', h: '時間', d: '日' },
};

function formatCompact(
  diffSeconds: number,
  now: string,
  round: (value: number) => number,
  units: CompactUnits
): string {
  const seconds = Math.abs(diffSeconds);
  if (seconds < 5) return now;
  if (seconds < 60) return `${Math.floor(seconds)}${units.s}`;
  // Rounding up must not give "60m" or "24h": the next unit takes over.
  if (round(seconds / 60) < 60) return `${round(seconds / 60)}${units.m}`;
  if (round(seconds / 3600) < 24) return `${round(seconds / 3600)}${units.h}`;
  return `${round(seconds / 86400)}${units.d}`;
}

// dayjs zh-cn writes "5 小时前"; Chinese puts no space between number and unit.
function longForm(text: string): string {
  return text.replace(/(\d)\s+(?=[\u4e00-\u9fff])/g, '$1');
}

/**
 * Renders a timestamp as a live-updating relative time ("3 minutes ago") with
 * the absolute time available in a tooltip.
 */
export function RelativeTime({
  timestamp,
  fallback = '-',
  compact = false,
  ago = false,
  stable = false,
  absolute,
  className,
}: Props) {
  const { locale, t, ts } = useI18n();
  const parsed = timestamp && timestamp !== '-' ? dayjs(timestamp) : null;
  const isValid = !!parsed && parsed.isValid();
  const now = t('time.now');
  const units = COMPACT_UNITS[locale] ?? EN_UNITS;
  // The text that reads the clock: the component renders only when it changes.
  const text = useClock(isValid ? 1000 : null, (nowMs) => {
    if (!parsed || !isValid) return '';
    return compact
      ? formatCompact(
          dayjs(nowMs).diff(parsed, 'second'),
          now,
          ago ? Math.round : Math.floor,
          units
        )
      : longForm(
          parsed
            .locale(locale === 'zh-CN' ? 'zh-cn' : locale)
            .from(dayjs(nowMs))
        );
  });

  if (!parsed || !isValid) {
    return <span className={className}>{fallback}</span>;
  }

  const lapse = compact ? text : undefined;
  const label = lapse
    ? ago && lapse !== now
      ? ts('{time} ago', { time: lapse })
      : lapse
    : text;

  // The widest text of the same unit has two digits: "99m", "99m ago".
  const widest =
    stable && lapse && lapse !== now
      ? ago
        ? ts('{time} ago', { time: lapse.replace(/\d+/, '99') })
        : lapse.replace(/\d+/, '99')
      : null;

  return (
    <span
      className={cn(widest && 'inline-block tabular-nums', className)}
      style={widest ? { minWidth: `${widest.length}ch` } : undefined}
      title={absolute ?? formatAbsoluteTime(timestamp) ?? undefined}
    >
      {label}
    </span>
  );
}

export default RelativeTime;
