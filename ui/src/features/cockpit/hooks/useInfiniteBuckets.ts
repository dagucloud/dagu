// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useConfig } from '@/contexts/ConfigContext';
import dayjs from '@/lib/dayjs';

const MAX_BUCKETS = 30;
const MAX_INTERVAL_DAYS = 30;

export interface KanbanBucket {
  key: string;
  fromStr: string;
  toStr: string;
  isLive: boolean;
}

function adjustTz(
  d: dayjs.Dayjs,
  tzOffsetInSec: number | undefined
): dayjs.Dayjs {
  return tzOffsetInSec !== undefined ? d.utcOffset(tzOffsetInSec / 60) : d;
}

// bucketAt returns the index-th bucket counting back from today (index 0 = the
// most recent `interval`-day window ending today). Each bucket spans the
// inclusive day range [toStr-(interval-1) .. toStr].
function bucketAt(
  today: dayjs.Dayjs,
  index: number,
  interval: number
): KanbanBucket {
  const to = today.subtract(index * interval, 'day');
  const from = to.subtract(interval - 1, 'day');
  const fromStr = from.format('YYYY-MM-DD');
  const toStr = to.format('YYYY-MM-DD');
  return { key: `${fromStr}_${toStr}`, fromStr, toStr, isLive: index === 0 };
}

/**
 * useInfiniteBuckets yields rolling N-day buckets newest-first and lets the
 * caller load older buckets on demand (capped at MAX_BUCKETS). It is the
 * bucket-stepped analog of useInfiniteKanban (which steps a single day).
 * `resetKey` resets the loaded window when filters or the interval change.
 */
export function useInfiniteBuckets(intervalDays: number, resetKey?: string) {
  const { tzOffsetInSec } = useConfig();
  const interval = Math.max(
    1,
    Math.min(Math.floor(intervalDays) || 1, MAX_INTERVAL_DAYS)
  );

  // Anchor "today" once per timezone so buckets don't shift between renders.
  const today = useMemo(
    () => adjustTz(dayjs(), tzOffsetInSec),
    [tzOffsetInSec]
  );

  const [count, setCount] = useState(1);

  const prevKeyRef = useRef(resetKey);
  const prevIntervalRef = useRef(interval);
  useEffect(() => {
    if (prevKeyRef.current !== resetKey || prevIntervalRef.current !== interval) {
      prevKeyRef.current = resetKey;
      prevIntervalRef.current = interval;
      setCount(1);
    }
  }, [resetKey, interval]);

  const buckets = useMemo(
    () =>
      Array.from({ length: count }, (_, i) => bucketAt(today, i, interval)),
    [count, today, interval]
  );

  const hasMore = count < MAX_BUCKETS;
  const loadNext = useCallback(() => {
    setCount((c) => Math.min(c + 1, MAX_BUCKETS));
  }, []);

  return { buckets, hasMore, loadNext };
}
