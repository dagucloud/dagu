import React, { useCallback, useEffect, useRef, useState } from 'react';
import { components } from '@/api/v1/schema';
import DAGRunDetailsModal from '@/features/dag-runs/components/dag-run-details/DAGRunDetailsModal';
import { useInfiniteKanban } from '../hooks/useInfiniteKanban';
import { ArtifactListModal } from './ArtifactListModal';
import { DateKanbanSection } from './DateKanbanSection';

type DAGRunSummary = components['schemas']['DAGRunSummary'];

interface Props {
  selectedWorkspace: string;
  workspaceKey?: string;
  suspendLoadMore?: boolean;
}

export function DateKanbanList({
  selectedWorkspace,
  workspaceKey = selectedWorkspace,
  suspendLoadMore = false,
}: Props): React.ReactElement {
  const { loadedDates, todayStr, hasMore, loadNextDate } =
    useInfiniteKanban(workspaceKey);
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

  const handleCloseModal = useCallback(() => {
    setIsDetailsOpen(false);
  }, []);

  const handleArtifactsClick = useCallback((run: DAGRunSummary) => {
    setArtifactRun(run);
  }, []);

  const handleCloseArtifactsModal = useCallback(() => {
    setArtifactRun(null);
  }, []);

  const triggerLoadNextDate = useCallback(() => {
    if (!hasMore || suspendLoadMore) {
      return;
    }
    awaitingSentinelExitRef.current = true;
    loadNextDate();
  }, [hasMore, loadNextDate, suspendLoadMore]);

  const dateCount = loadedDates.length;
  useEffect(() => {
    awaitingSentinelExitRef.current = false;
  }, [workspaceKey]);

  useEffect(() => {
    const root = containerRef.current;
    const el = sentinelRef.current;
    if (
      !root ||
      !el ||
      !hasMore ||
      suspendLoadMore ||
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
          triggerLoadNextDate();
        }
      },
      { root, threshold: 0.1 }
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [dateCount, hasMore, suspendLoadMore, triggerLoadNextDate]);

  return (
    <>
      <div
        ref={containerRef}
        className="flex flex-col overflow-y-auto flex-1 min-h-0 gap-6 p-1"
      >
        {loadedDates.map((date) => (
          <DateKanbanSection
            key={date}
            date={date}
            todayStr={todayStr}
            onCardClick={handleCardClick}
            onArtifactsClick={handleArtifactsClick}
          />
        ))}
        {hasMore && (
          <div className="flex flex-col items-center gap-3 pb-3">
            <button
              type="button"
              onClick={triggerLoadNextDate}
              disabled={suspendLoadMore}
              className="rounded border border-border px-3 py-1.5 text-xs text-muted-foreground hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
            >
              Load older day
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
        onClose={handleCloseModal}
      />
      <ArtifactListModal
        run={artifactRun}
        isOpen={!!artifactRun}
        onClose={handleCloseArtifactsModal}
      />
    </>
  );
}
