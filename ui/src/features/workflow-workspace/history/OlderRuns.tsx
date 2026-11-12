// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronsLeft, RotateCw } from 'lucide-react';
import { useEffect, useRef, type RefObject } from 'react';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/ui/icon-button';
import { Spinner } from '@/components/ui/spinner';
import { useI18n } from '@/i18n/I18nProvider';
import type { OlderRuns } from '../data/useOlderRuns';

// How far before the oldest end the next page starts to load.
const AHEAD_PX = 200;

const canObserve = () => typeof IntersectionObserver !== 'undefined';

/**
 * An invisible mark at the oldest end of a scrolled list of runs. In view of
 * `root`, or AHEAD_PX before, it reads the next page of older runs, and again
 * after each page while it stays in view. A failed read waits for a retry.
 */
export function OlderRunsTrigger({
  older,
  root,
  edge,
  className,
}: {
  older: OlderRuns;
  root: RefObject<HTMLElement | null>;
  edge: 'left' | 'bottom';
  className?: string;
}) {
  const ref = useRef<HTMLSpanElement>(null);
  const { hasMore, loading, failed, loadMore } = older;
  const armed = hasMore && !loading && !failed;
  useEffect(() => {
    const target = ref.current;
    if (!target || !armed || !canObserve()) return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) loadMore();
      },
      {
        root: root.current,
        rootMargin:
          edge === 'left'
            ? `0px 0px 0px ${AHEAD_PX}px`
            : `0px 0px ${AHEAD_PX}px 0px`,
      }
    );
    observer.observe(target);
    return () => observer.disconnect();
  }, [armed, edge, loadMore, root]);
  return <span ref={ref} aria-hidden="true" className={className} />;
}

/**
 * The state of the read of older runs: a spinner while it runs and a retry
 * when it failed. Without IntersectionObserver, a button reads the next
 * page. `compact` shows icons only, with their names as labels.
 */
export function OlderRunsStatus({
  older,
  compact = false,
}: {
  older: OlderRuns;
  compact?: boolean;
}) {
  const { ts } = useI18n();
  if (older.loading) {
    return compact ? (
      <Spinner className="size-3" label={ts('Loading older runs...')} />
    ) : (
      <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
        <Spinner className="size-3" />
        <span role="status">{ts('Loading older runs...')}</span>
      </span>
    );
  }
  if (older.failed) {
    return compact ? (
      <IconButton
        size="sm"
        icon={RotateCw}
        label={ts("Couldn't load older runs. Retry")}
        onClick={older.loadMore}
      />
    ) : (
      <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
        {ts("Couldn't load older runs")}
        <Button
          type="button"
          variant="ghost"
          size="xs"
          onClick={older.loadMore}
        >
          {ts('Retry')}
        </Button>
      </span>
    );
  }
  if (!older.hasMore || canObserve()) return null;
  return compact ? (
    <IconButton
      size="sm"
      icon={ChevronsLeft}
      label={ts('Load older')}
      onClick={older.loadMore}
    />
  ) : (
    <Button type="button" variant="ghost" size="xs" onClick={older.loadMore}>
      {ts('Load older')}
    </Button>
  );
}
