// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronsRight, Pin } from 'lucide-react';
import { useCallback } from 'react';
import { IconButton } from '@/components/ui/icon-button';
import RelativeTime from '@/components/ui/relative-time';
import { RefreshButton } from '@/components/ui/refresh-button';
import { StatusDot } from '@/components/ui/status-dot';
import { useI18n } from '@/i18n/I18nProvider';
import { runDisplayStatus } from '@/lib/runDisplayStatus';
import { statusLabelText } from '@/lib/status-utils';
import { cn } from '@/lib/utils';
import type { BarSlotProps, DAGPageData } from '../contracts';
import { useWorkspaceRefresh, useWorkspaceRun } from '../data';
import { useDockCommands } from '../dock/DockContext';
import { DAGIdentity } from './DAGIdentity';
import { LabelChips } from './LabelChips';
import { RunActionsCluster } from './RunActionsCluster';
import { RunChip } from './RunChip';
import { RunDetailsButton } from './RunDetailsPopover';
import { RunSplitButton } from './RunSplitButton';
import { RunStaleDot } from './RunStaleDot';
import { TriggerChip } from './TriggerChip';
import { WorkflowDeleteButton } from './WorkflowEditButtons';

export interface WorkflowBarProps {
  fileName: string;
  dagData: DAGPageData;
  workspaceName: string | null;
}

// Below md only the name with its Rename button, the run identity, the run
// icons, the Start button and the Delete button stay.
const SECONDARY = 'hidden min-w-0 items-center md:flex';

// The trigger chip is icons only below a bar of 1280px (@7xl): it keeps
// its width there, so that its icons never clip; the text may shrink after.
const TRIGGER_BAR =
  'hidden shrink-0 items-center md:not-empty:flex @7xl:min-w-0 @7xl:shrink';

// The bar is a size container. The run chip needs a bar of 1024px (@5xl);
// the side bar takes a part of the window, so a breakpoint of the window
// cannot tell. The labels need 672px (@2xl); below it the bar hides them. The
// labels and the trigger boxes are not there while they hold nothing: they
// leave no gap between the identity and the run chip.
const WIDE_BAR = 'hidden min-w-0 items-center @5xl:flex';
const LABELS_BAR =
  'hidden min-w-0 items-center overflow-hidden @2xl:not-empty:flex';

// The slot never gets less than the smallest chip: the identity and the
// labels shrink first. The run facts are in the tooltip of the chip and in
// the summary of the dock; a bar of 1152px (@[72rem]) also shows a row of them.
const RUN_CHIP_SLOT = 'min-w-min whitespace-nowrap';

/**
 * The run identity of a bar under 1024px: the status dot, the age of the
 * run and, when the run is not the latest, a pin. A click opens the dock on the
 * timeline, where the user picks a run. From a bar of 896px the two other ways
 * of the run chip stay: the stale dot, which toggles ?def=run, and, while a
 * run is pinned, an icon that goes back to the latest run, in place of the
 * pin. The slots of these parts are there, empty, when the part is absent: the
 * identity keeps its width. The Run details button is the last item.
 */
function CompactRunIdentity({
  fileName,
  dag,
}: Pick<BarSlotProps, 'fileName' | 'dag'>) {
  const { ts } = useI18n();
  const run = useWorkspaceRun();
  const details = run.details;
  const dock = useDockCommands();
  if (!details) return null;
  const pinned = run.selection !== 'latest';
  const display = runDisplayStatus(details);
  const statusName = display.healthy
    ? display.text
    : statusLabelText(details.statusLabel);
  const status = ts('Run status: {status}', { status: ts(statusName) });
  const label = pinned ? `${status}. ${ts('Pinned run')}` : status;
  return (
    <div
      data-testid="ww-bar-compact-run"
      className="flex shrink-0 items-center gap-1 @5xl:hidden"
    >
      <button
        type="button"
        data-testid="ww-bar-status-dot"
        aria-label={label}
        title={`${label}. ${ts('Open the run timeline')}`}
        onClick={() => dock.setTab('timeline', { user: true })}
        className="flex h-7 shrink-0 items-center gap-1 rounded-md px-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring @4xl:gap-1.5 @4xl:px-1.5"
      >
        <StatusDot
          kind="run"
          status={details.status}
          healthy={display.healthy}
          decorative
        />
        <RelativeTime
          timestamp={details.startedAt}
          compact
          ago
          fallback={ts(statusName)}
        />
        {/* From a bar of 896px the way back is the mark of a pinned run: the
            bar has no room for both. */}
        <span
          data-testid="ww-bar-compact-pin-slot"
          className="inline-flex size-3 shrink-0 @4xl:hidden"
        >
          {pinned && <Pin aria-hidden="true" className="size-3" />}
        </span>
      </button>
      {/* A bar under 896px has no room for them (a window of 1024px with the
          side bar open): the dock has the runs. The wrapper has no box, so
          that an empty one adds no gap. */}
      <div data-testid="ww-bar-compact-more" className="hidden @4xl:contents">
        <RunStaleDot fileName={fileName} dag={dag} compact />
        <span
          data-testid="ww-bar-compact-back-slot"
          className="inline-flex size-7 shrink-0 items-center justify-center"
        >
          {pinned && (
            <IconButton
              label={ts('Back to latest')}
              icon={ChevronsRight}
              onClick={() => run.select('latest')}
            />
          )}
        </span>
      </div>
      <RunDetailsButton details={details} compact />
    </div>
  );
}

/**
 * One row of 40px: identity (with the Rename button next to the name) and
 * chips, the run chip with the run icons right after it, then Start, Refresh
 * and Delete. The tab is one for the definition and its runs: the run chip
 * shows whenever the workflow has a run, and a bar under 1024px shows the
 * compact run identity in its place.
 */
export function WorkflowBar({
  fileName,
  dagData,
  workspaceName,
}: WorkflowBarProps) {
  const { ts } = useI18n();
  const refresh = useWorkspaceRefresh();
  const run = useWorkspaceRun();
  const hasRun = run.hasRun || run.selection !== 'latest';
  // A failed read shows in the data it feeds; the button only stops spinning.
  const onRefresh = useCallback(
    () => refresh().catch(() => undefined),
    [refresh]
  );

  const dag = dagData.dag;
  if (!dag) return null;
  const slot: BarSlotProps = { fileName, dag, workspaceName };

  return (
    <div
      data-testid="ww-bar"
      className="@container flex h-10 min-w-0 shrink-0 items-center gap-1.5 border-b border-border px-3"
    >
      <DAGIdentity {...slot} pinned={dagData.pinned} />
      <div data-testid="ww-bar-labels" className={LABELS_BAR}>
        <LabelChips {...slot} />
      </div>
      <div data-testid="ww-bar-trigger" className={TRIGGER_BAR}>
        <TriggerChip {...slot} />
      </div>
      <div
        data-testid="ww-bar-run"
        className="flex min-w-min shrink items-center gap-1 whitespace-nowrap"
      >
        {hasRun && (
          <div
            data-testid="ww-bar-run-chip"
            className={cn(WIDE_BAR, RUN_CHIP_SLOT)}
          >
            <RunChip {...slot} />
          </div>
        )}
        <CompactRunIdentity fileName={fileName} dag={dag} />
        {/* The run icons never shrink: a long name or label gives way first,
            so that no icon is cut by a pixel. */}
        <div
          data-testid="ww-bar-run-actions"
          className="flex shrink-0 items-center"
        >
          <RunActionsCluster {...slot} />
        </div>
      </div>
      <div className="ml-auto flex shrink-0 items-center gap-1">
        <RunSplitButton {...slot} />
        <div data-testid="ww-bar-refresh" className={SECONDARY}>
          <RefreshButton
            onRefresh={onRefresh}
            className="size-7"
            description={ts(
              'Reload the workflow, its runs and the latest run from the server'
            )}
          />
        </div>
        <WorkflowDeleteButton {...slot} />
      </div>
    </div>
  );
}
