import React, { useEffect, useRef } from 'react';
import { AnimatePresence, LayoutGroup } from 'framer-motion';
import { components } from '@/api/v1/schema';
import { KanbanCard } from './KanbanCard';
import type { KanbanColumnData } from '../hooks/useDateKanbanData';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';

type DAGRunSummary = components['schemas']['DAGRunSummary'];

interface Props {
  title: string;
  column: KanbanColumnData;
  onCardClick: (run: DAGRunSummary) => void;
  onArtifactsClick: (run: DAGRunSummary) => void;
  hideHeader?: boolean;
}

export function KanbanColumn({
  title,
  column,
  onCardClick,
  onArtifactsClick,
  hideHeader,
}: Props): React.ReactElement {
  const { ts } = useI18n();
  const scrollRef = useRef<HTMLDivElement>(null);
  const sentinelRef = useRef<HTMLDivElement>(null);
  const {
    error,
    hasMore,
    isInitialLoading,
    isLoadingMore,
    loadMore,
    loadMoreError,
    retry,
    runs,
  } = column;
  const visibleCountLabel = `${runs.length}${hasMore ? '+' : ''}`;
  const localizedTitle = ts(title);

  useEffect(() => {
    const root = scrollRef.current;
    const el = sentinelRef.current;
    if (
      !root ||
      !el ||
      !hasMore ||
      isLoadingMore ||
      typeof IntersectionObserver === 'undefined'
    ) {
      return;
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) {
          void loadMore();
        }
      },
      { root, threshold: 0.1 }
    );

    observer.observe(el);
    return () => observer.disconnect();
  }, [hasMore, isLoadingMore, loadMore, runs.length]);

  return (
    <section
      aria-label={ts('{title} column', { title: localizedTitle })}
      className="flex min-h-0 min-w-0 flex-1 flex-col"
    >
      {!hideHeader && (
        <div className="flex items-center gap-2 px-1 pb-2">
          <span className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            {localizedTitle}
          </span>
          <span className="text-[11px] text-muted-foreground/60">
            {visibleCountLabel}
          </span>
        </div>
      )}
      <div
        ref={scrollRef}
        className="flex flex-col gap-1.5 overflow-y-auto min-h-0 flex-1 px-0.5"
      >
        {runs.length === 0 && isInitialLoading ? (
          <div className="px-1 py-2 text-xs text-muted-foreground">
            <I18nText text={'Loading...'} />
          </div>
        ) : runs.length === 0 && error ? (
          <div className="px-1 py-2 text-xs">
            <div className="text-destructive">{error.message}</div>
            <button
              type="button"
              onClick={() => void retry()}
              className="mt-2 rounded border border-border px-2 py-1 text-muted-foreground hover:text-foreground"
            >
              <I18nText text={'Retry'} />
            </button>
          </div>
        ) : (
          <>
            <LayoutGroup>
              <AnimatePresence mode="popLayout">
                {runs.map((run) => (
                  <KanbanCard
                    key={run.dagRunId}
                    run={run}
                    onClick={() => onCardClick(run)}
                    onArtifactsClick={() => onArtifactsClick(run)}
                  />
                ))}
              </AnimatePresence>
            </LayoutGroup>
            {hasMore && (
              <div className="flex flex-col items-center gap-2 py-2">
                <button
                  type="button"
                  onClick={() => void loadMore()}
                  disabled={isLoadingMore}
                  className="rounded border border-border px-2 py-1 text-[11px] text-muted-foreground hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {isLoadingMore ? (
                    <I18nText text={'Loading...'} />
                  ) : (
                    <I18nText text={'Load more'} />
                  )}
                </button>
                <div ref={sentinelRef} className="h-1 w-full shrink-0" />
              </div>
            )}
            {loadMoreError && (
              <div className="px-1 pb-2 text-[11px] text-destructive">
                {loadMoreError}
              </div>
            )}
          </>
        )}
      </div>
    </section>
  );
}
