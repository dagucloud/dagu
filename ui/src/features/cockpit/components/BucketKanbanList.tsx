// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useCallback, useEffect, useRef, useState } from 'react';
import { components } from '@/api/v1/schema';
import DAGRunDetailsModal from '@/features/dag-runs/components/dag-run-details/DAGRunDetailsModal';
import type { KanbanFilters } from '../hooks/useDateKanbanData';
import { useInfiniteBuckets } from '../hooks/useInfiniteBuckets';
import { ArtifactListModal } from './ArtifactListModal';
import { BucketKanbanSection } from './BucketKanbanSection';

type DAGRunSummary = components['schemas']['DAGRunSummary'];

interface Props {
  intervalDays: number;
  filters: KanbanFilters;
  resetKey?: string;
}

/**
 * Renders a saved view's Kanban as rolling N-day buckets (one board per
 * `intervalDays`-day window), newest first, with infinite scroll back in time.
 * Mirrors the Cockpit DateKanbanList scroll/observer behavior, stepped by the
 * configured interval instead of a single day.
 */
export function BucketKanbanList({
  intervalDays,
  filters,
  resetKey,
}: Props): React.ReactElement {
  const { buckets, hasMore, loadNext } = useInfiniteBuckets(
    intervalDays,
    resetKey
  );
  const [selectedRun, setSelectedRun] = useState<DAGRunSummary | null>(null);
  const [isDetailsOpen, setIsDetailsOpen] = useState(false);
  const [artifactRun, setArtifactRun] = useState<DAGRunSummary | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const sentinelRef = useRef<HTMLDivElement>(null);
  const awaitingSentinelExitRef = useRef(false);

  const handleCardClick = useCallback((run: DAGRunSummary) => {
    setSelectedRun(run);
    setIsDetailsOpen(true);
  }, []);

  const handleArtifactsClick = useCallback((run: DAGRunSummary) => {
    setArtifactRun(run);
  }, []);

  const triggerLoadNext = useCallback(() => {
    if (!hasMore) {
      return;
    }
    awaitingSentinelExitRef.current = true;
    loadNext();
  }, [hasMore, loadNext]);

  useEffect(() => {
    awaitingSentinelExitRef.current = false;
  }, [resetKey]);

  const bucketCount = buckets.length;
  useEffect(() => {
    const root = containerRef.current;
    const el = sentinelRef.current;
    if (
      !root ||
      !el ||
      !hasMore ||
      typeof IntersectionObserver === 'undefined'
    ) {
      return;
    }
    const observer = new IntersectionObserver(
      ([entry]) => {
        const isIntersecting = !!entry?.isIntersecting;
        if (!isIntersecting) {
          awaitingSentinelExitRef.current = false;
          return;
        }
        if (!awaitingSentinelExitRef.current) {
          triggerLoadNext();
        }
      },
      { root, threshold: 0.1 }
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [bucketCount, hasMore, triggerLoadNext]);

  return (
    <>
      <div
        ref={containerRef}
        className="flex flex-col overflow-y-auto flex-1 min-h-0 gap-6 p-1"
      >
        {buckets.map((bucket) => (
          <BucketKanbanSection
            key={bucket.key}
            fromStr={bucket.fromStr}
            toStr={bucket.toStr}
            isLive={bucket.isLive}
            filters={filters}
            onCardClick={handleCardClick}
            onArtifactsClick={handleArtifactsClick}
          />
        ))}
        {hasMore && (
          <div className="flex flex-col items-center gap-3 pb-3">
            <button
              type="button"
              onClick={triggerLoadNext}
              className="rounded border border-border px-3 py-1.5 text-xs text-muted-foreground hover:text-foreground"
            >
              Load older
            </button>
            <div ref={sentinelRef} className="h-1 w-full shrink-0" />
          </div>
        )}
      </div>
      <DAGRunDetailsModal
        name={selectedRun?.name ?? ''}
        dagRunId={selectedRun?.dagRunId ?? ''}
        isOpen={isDetailsOpen && !!selectedRun}
        initialTab={selectedRun?.artifactsAvailable ? 'artifacts' : 'status'}
        onClose={() => setIsDetailsOpen(false)}
      />
      <ArtifactListModal
        run={artifactRun}
        isOpen={!!artifactRun}
        onClose={() => setArtifactRun(null)}
      />
    </>
  );
}
