// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import type { components } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';

type RunDateValue = components['schemas']['RunDateValue'];

const READ_ONCE = {
  revalidateOnFocus: false,
  revalidateIfStale: false,
  refreshInterval: 0,
} as const;

/** How long a typed slot rests before it is previewed. */
const PREVIEW_DELAY_MS = 400;

/** The text of a date: its value, or a list's values. */
export function dateText(date: RunDateValue): string {
  return date.list ? (date.values ?? []).join(', ') || '—' : (date.value ?? '');
}

/**
 * The slot a start would get, its window, and the dates its run would get,
 * read from the server as the start would resolve them. It reports the
 * reason when the dates cannot resolve, so the dialog can hold the start.
 * The preview is read when the slot or profile changes, a typed slot after
 * it rests.
 */
export function SlotDatesPreview({
  fileName,
  remoteNode,
  scheduledAt,
  profile,
  onBlocked,
}: {
  fileName: string;
  remoteNode: string;
  /** The slot as the start API takes it, or undefined for the default. */
  scheduledAt?: string;
  /** The profile override a start would send: undefined for the DAG default,
   * empty for no profile. */
  profile?: string;
  onBlocked: (reason: string | null) => void;
}) {
  const { ts } = useI18n();
  const [settled, setSettled] = React.useState(scheduledAt);
  React.useEffect(() => {
    const timer = setTimeout(() => setSettled(scheduledAt), PREVIEW_DELAY_MS);
    return () => clearTimeout(timer);
  }, [scheduledAt]);
  const { data, error } = useQuery(
    '/dags/{fileName}/slot-preview',
    whenEnabled(settled === scheduledAt, {
      params: {
        path: { fileName },
        query: {
          remoteNode,
          ...(settled ? { scheduledAt: settled } : {}),
          ...(profile !== undefined ? { profile } : {}),
        },
      },
    }),
    READ_ONCE
  );
  const reason = error
    ? typeof error === 'object' && error && 'message' in error
      ? String((error as { message: unknown }).message)
      : ts('The dates of this slot cannot be resolved.')
    : null;
  React.useEffect(() => onBlocked(reason), [reason, onBlocked]);

  if (reason) {
    return (
      <p role="alert" className="text-xs text-destructive">
        {reason}
      </p>
    );
  }
  if (!data || data.dates.length === 0) return null;
  const time = (value?: string) =>
    value ? (formatAbsoluteTime(value, { seconds: false }) ?? value) : '';
  return (
    <div
      aria-label={ts('Dates of this run')}
      className="space-y-1 rounded-md border bg-muted/30 px-2 py-1.5 text-xs"
    >
      {data.scheduledAt && (
        <p className="text-muted-foreground">
          {data.defaultSlot
            ? ts('Slot {time}, the latest fire', {
                time: time(data.scheduledAt),
              })
            : ts('Slot {time}', { time: time(data.scheduledAt) })}
          {data.windowStart &&
            ` · ${ts('window from {time}', { time: time(data.windowStart) })}`}
        </p>
      )}
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
        {data.dates.map((date) => (
          <React.Fragment key={date.name}>
            <dt className="font-mono text-muted-foreground">{date.name}</dt>
            <dd className="font-mono tabular-nums">{dateText(date)}</dd>
          </React.Fragment>
        ))}
      </dl>
    </div>
  );
}
