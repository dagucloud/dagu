// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, useState } from 'react';
import { DAGSlotStatus } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import dayjs from '@/lib/dayjs';
import type { BackfillTarget } from './StartBackfillDialog';

/** How far back the panel looks for the last success. */
const SUCCESS_LOOKBACK_DAYS = 90;

type Choice = '7d' | '30d' | 'since-success' | 'custom';

const READ_ONCE = {
  revalidateOnFocus: false,
  revalidateIfStale: false,
  refreshInterval: 0,
} as const;

/** The range a preset of days back from now selects. */
export function daysBack(days: number): BackfillTarget {
  return { kind: 'range', from: dayjs().subtract(days, 'day').toISOString() };
}

/** The leading wall time of an RFC 3339 time, as a time input shows it. */
const WALL_TIME = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}/;

/** The offset an RFC 3339 time ends with, such as Z or +09:00. */
const OFFSET = /(Z|[+-]\d{2}:\d{2})$/;

/** A time input's value, which has no seconds. */
const MINUTE_TIME = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/;

/**
 * A bound as a time input shows it. A bound with an offset shows its wall
 * time in that offset.
 */
function inputValue(bound: string): string {
  return WALL_TIME.exec(bound)?.[0] ?? bound;
}

/** A typed time read in the given offset, or in the schedule's zone. */
function inOffset(value: string, offset: string | undefined): string {
  if (!value || !offset) return value;
  return `${MINUTE_TIME.test(value) ? `${value}:00` : value}${offset}`;
}

/**
 * Picks the range of a backfill: the last 7 or 30 days, since the last
 * success, or fire times typed in. Times typed without an offset are read in
 * the schedule's zone. A given initial range starts as fire times, and each
 * of its bounds is kept as given until edited; a bound typed then is read
 * in the offset the initial range carries. The DAG's slots are read once,
 * to find the last success, when the panel opens.
 */
export function BackfillRangePicker({
  fileName,
  remoteNode,
  initial,
  onChange,
}: {
  fileName: string;
  remoteNode: string;
  initial?: { from: string; to?: string };
  onChange: (target: BackfillTarget | null) => void;
}) {
  const { ts } = useI18n();
  const [choice, setChoice] = useState<Choice>(initial ? 'custom' : '7d');
  const [from, setFrom] = useState(initial?.from ?? '');
  const [to, setTo] = useState(initial?.to ?? '');
  const [offset] = useState(() =>
    initial ? OFFSET.exec(initial.from)?.[1] : undefined
  );
  const lookback = useMemo(
    () => dayjs().subtract(SUCCESS_LOOKBACK_DAYS, 'day').toISOString(),
    []
  );
  const { data, error, mutate } = useQuery(
    '/dags/{fileName}/slots',
    { params: { path: { fileName }, query: { remoteNode, from: lookback } } },
    READ_ONCE
  );
  const lastSuccess = useMemo(() => {
    const succeeded = (data?.slots ?? []).filter(
      (slot) => slot.status === DAGSlotStatus.succeeded
    );
    return succeeded[succeeded.length - 1]?.slot;
  }, [data]);

  const pick = (next: Choice) => {
    setChoice(next);
    switch (next) {
      case '7d':
        onChange(daysBack(7));
        break;
      case '30d':
        onChange(daysBack(30));
        break;
      case 'since-success':
        onChange(
          lastSuccess
            ? {
                kind: 'range',
                from: dayjs(lastSuccess).add(1, 'minute').toISOString(),
              }
            : null
        );
        break;
      case 'custom':
        onChange(from ? { kind: 'range', from, ...(to ? { to } : {}) } : null);
        break;
    }
  };
  const typed = (nextFrom: string, nextTo: string) => {
    setFrom(nextFrom);
    setTo(nextTo);
    onChange(
      nextFrom
        ? { kind: 'range', from: nextFrom, ...(nextTo ? { to: nextTo } : {}) }
        : null
    );
  };

  const options: { value: Choice; label: string; disabled?: boolean }[] = [
    { value: '7d', label: ts('Last 7 days') },
    { value: '30d', label: ts('Last 30 days') },
    {
      value: 'since-success',
      label: lastSuccess
        ? ts('Since the last success, {time}', {
            time:
              formatAbsoluteTime(lastSuccess, { seconds: false }) ??
              lastSuccess,
          })
        : ts('Since the last success'),
      disabled: !lastSuccess,
    },
    { value: 'custom', label: ts('Fire times') },
  ];

  return (
    <fieldset className="flex flex-col gap-1.5">
      <legend className="mb-1 font-medium">{ts('Slots')}</legend>
      {options.map((option) => (
        <label
          key={option.value}
          className="flex cursor-pointer items-center gap-2 aria-disabled:cursor-not-allowed aria-disabled:text-muted-foreground"
          aria-disabled={option.disabled}
        >
          <input
            type="radio"
            name="backfill-range"
            checked={choice === option.value}
            disabled={option.disabled}
            onChange={() => pick(option.value)}
          />
          {option.label}
        </label>
      ))}
      {error ? (
        <span
          role="alert"
          className="flex items-center gap-2 text-xs text-destructive"
        >
          {ts('Could not read the last success: {message}', {
            message:
              typeof error === 'object' && error && 'message' in error
                ? String((error as { message: unknown }).message)
                : String(error),
          })}
          <Button variant="ghost" size="xs" onClick={() => void mutate()}>
            {ts('Retry')}
          </Button>
        </span>
      ) : (
        !lastSuccess &&
        data && (
          <span className="text-xs text-muted-foreground">
            {ts('No slot succeeded in the last {days} days.', {
              days: SUCCESS_LOOKBACK_DAYS,
            })}
          </span>
        )
      )}
      {choice === 'custom' && (
        <div className="flex flex-wrap items-center gap-2 pl-6">
          <Input
            type="datetime-local"
            aria-label={ts('First fire time')}
            className="w-52"
            value={inputValue(from)}
            onChange={(event) =>
              typed(inOffset(event.target.value, offset), to)
            }
          />
          <span aria-hidden="true" className="text-muted-foreground">
            –
          </span>
          <Input
            type="datetime-local"
            aria-label={ts('Last fire time')}
            className="w-52"
            value={inputValue(to)}
            onChange={(event) =>
              typed(from, inOffset(event.target.value, offset))
            }
          />
          {offset && (
            <span className="text-xs text-muted-foreground">
              {ts('Times are in {zone}.', {
                zone: offset === 'Z' ? 'UTC' : `UTC${offset}`,
              })}
            </span>
          )}
        </div>
      )}
    </fieldset>
  );
}
