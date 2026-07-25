// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import { shouldIgnoreKeyboardShortcuts } from '@/lib/keyboard-shortcuts';
import { cn } from '@/lib/utils';
import { ChevronRight, Loader2, X } from 'lucide-react';
import React from 'react';

// Loaded lazily: the content renders DAGStatus, which is what opens this modal.
// A static import would close that cycle at module load.
const DAGRunDetailsContent = React.lazy(
  () =>
    import(
      '@/features/dag-runs/components/dag-run-details/DAGRunDetailsContent'
    )
);

/** One child DAG-run on the drill-down stack. */
export type SubRunStackEntry = {
  /** Child DAG name, used as the breadcrumb label. */
  name: string;
  /** Child DAG-run ID. */
  dagRunId: string;
};

/**
 * Lets a nested DAGStatus push onto the stack that is already open, instead of
 * opening a second modal inside the first. The stack stays flat however deep
 * the drill-down goes.
 */
const SubRunStackContext = React.createContext<
  ((entry: SubRunStackEntry) => void) | null
>(null);

export function useSubRunStackPush() {
  return React.useContext(SubRunStackContext);
}

/** How many previous levels show an edge before the count takes over. */
const MAX_VISIBLE_EDGES = 3;
/** Width of each peeking edge, in pixels. */
const EDGE_WIDTH = 12;

type SubRunStackModalProps = {
  /** Root DAG-run that owns every child on the stack. */
  rootName: string;
  rootDAGRunId: string;
  /** Breadcrumb label for the root. */
  rootLabel: string;
  stack: SubRunStackEntry[];
  onChange: (next: SubRunStackEntry[]) => void;
};

/**
 * Drill-down viewer for child DAG-runs. Opening a child pushes onto a stack
 * rather than navigating, so the run you started from is never lost, and the
 * depth you are at stays visible.
 */
export function SubRunStackModal({
  rootName,
  rootDAGRunId,
  rootLabel,
  stack,
  onChange,
}: SubRunStackModalProps): React.ReactElement | null {
  const remoteNode = useRemoteNode();
  const top = stack[stack.length - 1];
  const previousRef = React.useRef<
    components['schemas']['DAGRunDetails'] | null
  >(null);

  const { data, refresh, isValidating } = useBoundedDAGRunDetails({
    target: top
      ? {
          remoteNode,
          name: top.name,
          dagRunId: top.dagRunId,
          parentName: rootName,
          parentDAGRunId: rootDAGRunId,
          subDAGRunId: top.dagRunId,
        }
      : null,
    enabled: !!top,
    pollIntervalMs: top ? 2000 : 0,
  });

  const fresh = data?.dagRunId === top?.dagRunId ? data : null;
  React.useEffect(() => {
    if (fresh) previousRef.current = fresh;
  }, [fresh]);
  // Keep the last good render while the next level loads, so drilling in never
  // blanks the panel.
  const shown = fresh ?? previousRef.current;

  const popTo = React.useCallback(
    (depth: number) => {
      previousRef.current = null;
      onChange(stack.slice(0, depth));
    },
    [onChange, stack]
  );

  const push = React.useCallback(
    (entry: SubRunStackEntry) => {
      previousRef.current = null;
      onChange([...stack, entry]);
    },
    [onChange, stack]
  );

  React.useEffect(() => {
    if (!top) return;
    function onKeyDown(event: KeyboardEvent) {
      if (shouldIgnoreKeyboardShortcuts()) return;
      if (event.key === 'Escape') {
        event.stopPropagation();
        popTo(stack.length - 1);
      }
    }
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [top, stack.length, popTo]);

  if (!top) return null;

  const depth = stack.length;
  const edges = Math.min(depth - 1, MAX_VISIBLE_EDGES);
  const hiddenLevels = depth - 1 - edges;

  return (
    <>
      <div
        className="fixed inset-0 z-40 h-screen w-screen bg-black/20"
        onClick={() => popTo(0)}
      />

      {/* Edges of the levels underneath, newest nearest the panel. */}
      {Array.from({ length: edges }).map((_, i) => {
        const level = depth - 1 - i;
        return (
          <button
            key={level}
            type="button"
            title={`Back to ${stack[level - 1]?.name ?? rootLabel}`}
            onClick={() => popTo(level)}
            style={{ right: `calc(75% - ${(i + 1) * EDGE_WIDTH}px)` }}
            className="bg-card hover:bg-muted border-border fixed top-0 bottom-0 z-40 h-screen border-l transition-colors"
            aria-label={`Back one level`}
          >
            <span className="sr-only">Back</span>
            <span className="block" style={{ width: EDGE_WIDTH }} />
          </button>
        );
      })}

      <div className="bg-background fixed top-0 right-0 bottom-0 z-50 flex h-screen w-full flex-col border-l border-indigo-500/30 md:w-3/4">
        <div className="border-border flex items-center gap-2 border-b px-3 py-2">
          <nav className="flex min-w-0 flex-1 flex-wrap items-center gap-x-1 text-xs">
            <button
              type="button"
              onClick={() => popTo(0)}
              className="text-muted-foreground hover:text-foreground cursor-pointer"
            >
              {rootLabel}
            </button>
            {hiddenLevels > 0 ? (
              <>
                <ChevronRight className="text-muted-foreground/50 h-3 w-3" />
                <span className="text-muted-foreground">
                  +{hiddenLevels} more
                </span>
              </>
            ) : null}
            {stack.map((entry, index) => {
              const isLast = index === stack.length - 1;
              if (hiddenLevels > 0 && index < hiddenLevels) return null;
              return (
                <React.Fragment key={`${entry.dagRunId}-${index}`}>
                  <ChevronRight className="text-muted-foreground/50 h-3 w-3" />
                  <button
                    type="button"
                    disabled={isLast}
                    onClick={() => popTo(index + 1)}
                    className={cn(
                      'max-w-[16rem] truncate',
                      isLast
                        ? 'text-foreground font-medium'
                        : 'text-muted-foreground hover:text-foreground cursor-pointer'
                    )}
                  >
                    {entry.name}
                  </button>
                </React.Fragment>
              );
            })}
          </nav>

          {isValidating ? (
            <Loader2 className="text-muted-foreground h-3.5 w-3.5 animate-spin" />
          ) : null}
          <span className="text-muted-foreground bg-muted rounded px-1.5 py-0.5 text-xs tabular-nums">
            {depth}
          </span>
          <Button
            variant="outline"
            size="icon"
            onClick={() => popTo(stack.length - 1)}
            title="Back one level (Esc)"
            className="h-7 w-7"
          >
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>

        <div className="min-h-0 flex-1 overflow-auto p-4">
          <SubRunStackContext.Provider value={push}>
            <React.Suspense fallback={<LoadingIndicator />}>
              {shown ? (
                <DAGRunDetailsContent
                  name={top.name}
                  dagRunId={top.dagRunId}
                  dagRun={shown}
                  refreshFn={refresh}
                  fillHeight
                />
              ) : (
                <LoadingIndicator />
              )}
            </React.Suspense>
          </SubRunStackContext.Provider>
        </div>
      </div>
    </>
  );
}
