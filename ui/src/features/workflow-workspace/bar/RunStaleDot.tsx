// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Hash } from 'lucide-react';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import {
  useRunSpecDiff,
  useRunSpecStale,
} from '@/features/visual-editor/run/useRunSpecStale';
import { useI18n } from '@/i18n/I18nProvider';
import { toneClasses } from '@/lib/statusPalette';
import { cn } from '@/lib/utils';
import type { BarSlotProps } from '../contracts';
import { useWorkspaceRun } from '../data';
import { useWorkspaceUrl } from '../state/workspaceUrl';

/** The id of the run and of the file that the stale check reads. */
function useSpecRef(fileName: string, dag: BarSlotProps['dag']) {
  const run = useWorkspaceRun();
  return {
    dagName: run.ref?.name ?? dag.name,
    fileName,
    runId: run.ref?.runId ?? null,
  };
}

/**
 * The button of the warning dot: nothing while the run is current. A click
 * toggles ?def=run, the definition of the run on the canvas.
 */
function StaleButton({
  fileName,
  dag,
  compact = false,
  className,
}: Pick<BarSlotProps, 'fileName' | 'dag'> & {
  compact?: boolean;
  /** The size of the hit area. */
  className?: string;
}) {
  const { ts } = useI18n();
  const [url, setUrl] = useWorkspaceUrl();
  const specRef = useSpecRef(fileName, dag);
  const stale = useRunSpecStale(specRef);
  const diff = useRunSpecDiff(specRef);
  const showingRunDef = url.def === 'run';
  // The canvas shows the run definition under ?def=run: no step is missing.
  const notOnCanvas = showingRunDef ? 0 : (diff?.removedFromLive.size ?? 0);
  if (!stale) return null;
  const staleText =
    notOnCanvas === 0
      ? ts('This run used a different definition.')
      : ts(
          notOnCanvas === 1
            ? 'This run used a different definition. {count} step of the run is not on the canvas.'
            : 'This run used a different definition. {count} steps of the run are not on the canvas.',
          { count: notOnCanvas }
        );
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          data-testid={
            compact ? 'ww-bar-compact-stale-dot' : 'ww-run-stale-dot'
          }
          className={cn(
            'inline-flex size-5 shrink-0 items-center justify-center rounded-full hover:bg-muted',
            showingRunDef && 'bg-muted',
            className
          )}
          aria-label={staleText}
          aria-pressed={showingRunDef}
          onClick={() => setUrl({ def: showingRunDef ? null : 'run' })}
        >
          <span
            aria-hidden="true"
            className={cn('size-2 rounded-full', toneClasses('warning').dot)}
          />
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom" className="max-w-xs text-left">
        <p>{staleText}</p>
        <p className="text-muted-foreground">
          {ts(
            showingRunDef
              ? 'Back to the current definition'
              : 'Show the definition of this run'
          )}
        </p>
      </TooltipContent>
    </Tooltip>
  );
}

/**
 * The warning dot of a run that used a different definition than the saved
 * one, in a slot of the size of the dot. The slot is there, empty, while the
 * run is current: the compact run identity, and the run chip of a bar that
 * has no row of facts, do not change their width when the dot appears.
 */
export function RunStaleDot({
  fileName,
  dag,
  compact = false,
  className,
}: Pick<BarSlotProps, 'fileName' | 'dag'> & {
  /** Classes of the slot, such as its place in the flex row of the chip. */
  className?: string;
  /**
   * The dot of the compact identity: the chip and it are both in the DOM, so
   * the test ID differs.
   */
  compact?: boolean;
}) {
  return (
    <span
      data-testid={compact ? 'ww-bar-compact-stale-slot' : 'ww-run-stale-slot'}
      className={cn(
        'inline-flex size-5 shrink-0 items-center justify-center',
        className
      )}
    >
      <StaleButton fileName={fileName} dag={dag} compact={compact} />
    </span>
  );
}

/**
 * The mark in front of the run ID in the row of facts: the # of a run that
 * used the saved definition, the warning dot of a run that used another one.
 * The dot does what it does elsewhere: a click toggles ?def=run.
 */
export function RunIdMark({
  fileName,
  dag,
}: Pick<BarSlotProps, 'fileName' | 'dag'>) {
  const specRef = useSpecRef(fileName, dag);
  const stale = useRunSpecStale(specRef);
  return stale ? (
    <StaleButton fileName={fileName} dag={dag} className="-mx-1 size-5" />
  ) : (
    <Hash className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
  );
}
