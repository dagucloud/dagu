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
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

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
 * Opens a child DAG-run in the stack. Provided by whichever view owns the
 * stack, so a step table, a graph node, or an agent timeline all drill down
 * the same way, and a nested view pushes onto the stack already open rather
 * than opening a second one.
 */
const SubRunOpenContext = React.createContext<
  ((entry: SubRunStackEntry) => void) | null
>(null);

/**
 * Returns the opener when a stack is available, or null when the view is not
 * inside one and should fall back to navigation.
 */
export function useOpenSubRun() {
  return React.useContext(SubRunOpenContext);
}

export const SubRunOpenProvider = SubRunOpenContext.Provider;

/** How many previous levels show an edge before the count takes over. */
const MAX_VISIBLE_EDGES = 3;
/** Width of each peeking edge, in pixels. */
const EDGE_WIDTH = 12;
/** Slide duration in ms. Kept short so drilling in never feels like waiting. */
const SLIDE_MS = 90;

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
  const { ts } = useI18n();
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

  const [isVisible, setIsVisible] = React.useState(false);
  React.useEffect(() => {
    const frame = requestAnimationFrame(() => setIsVisible(true));
    return () => cancelAnimationFrame(frame);
  }, []);

  const popTo = React.useCallback(
    (depth: number) => {
      previousRef.current = null;
      if (depth > 0) {
        // Popping swaps content in place, which needs no transition.
        onChange(stack.slice(0, depth));
        return;
      }
      // Closing slides back out before the panel leaves the tree.
      setIsVisible(false);
      window.setTimeout(() => onChange([]), SLIDE_MS);
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
      {/* Click-away target only: the run underneath stays fully legible. */}
      <div
        className="fixed inset-0 z-40 h-screen w-screen"
        onClick={() => popTo(0)}
      />

      {/* Edges of the levels underneath, newest nearest the panel. */}
      {Array.from({ length: edges }).map((_, i) => {
        const level = depth - 1 - i;
        return (
          <button
            key={level}
            type="button"
            title={ts('Back to {name}', {
              name: stack[level - 1]?.name ?? rootLabel,
            })}
            onClick={() => popTo(level)}
            className={cn(
              'bg-card hover:bg-muted border-border fixed top-0 bottom-0 z-40 h-screen border-l transition-all ease-out',
              !isVisible && 'translate-x-full opacity-0'
            )}
            style={{
              right: `calc(75% - ${(i + 1) * EDGE_WIDTH}px)`,
              transitionDuration: `${SLIDE_MS}ms`,
            }}
            aria-label={ts('Back one level')}
          >
            <span className="sr-only">
              <I18nText text={'Back'} />
            </span>
            <span className="block" style={{ width: EDGE_WIDTH }} />
          </button>
        );
      })}

      <div
        className={cn(
          'bg-background fixed top-0 right-0 bottom-0 z-50 flex h-screen w-full flex-col border-l border-indigo-500/30 transition-transform ease-out md:w-3/4',
          !isVisible && 'translate-x-full'
        )}
        style={{ transitionDuration: `${SLIDE_MS}ms` }}
      >
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
                  {ts('+{count} more', { count: hiddenLevels })}
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
          <I18nProps>
            <Button
              variant="outline"
              size="icon"
              onClick={() => popTo(stack.length - 1)}
              title="Back one level (Esc)"
              className="h-7 w-7"
            >
              <X className="h-3.5 w-3.5" />
            </Button>
          </I18nProps>
        </div>

        <div className="min-h-0 flex-1 overflow-auto p-4">
          <SubRunOpenProvider value={push}>
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
          </SubRunOpenProvider>
        </div>
      </div>
    </>
  );
}
