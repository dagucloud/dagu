// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useMemo, useState } from 'react';
import {
  AlertTriangle,
  ChevronLeft,
  ChevronLast,
  ChevronRight,
  FlaskConical,
} from 'lucide-react';
import { NodeStatus, Status } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/spinner';
import StatusIcon from '@/components/ui/status-icon';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { RepeatBadge } from '@/components/ui/repeat-badge';
import { readRepeatPolicy } from '@/components/ui/repeat-summary';
import { isEphemeralRun } from '@/features/visual-editor/run/inputProvenance';
import AutoRetryBadge from '@/features/dag-runs/components/common/AutoRetryBadge';
import { useI18n } from '@/i18n/I18nProvider';
import { runDisplayStatus } from '@/lib/runDisplayStatus';
import { readableToneText, toneClasses } from '@/lib/statusPalette';
import { cn } from '@/lib/utils';
import type { BarSlotProps } from '../contracts';
import { useWorkspaceRun } from '../data';
import { useVisibleRuns } from '../data/useVisibleRuns';
import { isNewerRun } from '../data/runFacts';
import { useDockCommands } from '../dock/DockContext';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { RunFacts, hasHiddenFacts, visibleFacts } from './RunFacts';
import { RunIdMark, RunStaleDot } from './RunStaleDot';
import { factsShownAt, useBarWidth } from './useFactTiers';

// The statuses for which isRunActive is true.
const ACTIVE_STATUSES: ReadonlySet<Status> = new Set([
  Status.NotStarted,
  Status.Queued,
  Status.Running,
  Status.Waiting,
]);

type Direction = 'older' | 'newer';

// The slot of the count holds "99/99" or the repeat badge with "×1.2M". Its
// width is in ch (of the 12px text), so that it follows the font.
const COUNT_SLOT =
  'inline-flex w-[8ch] shrink-0 items-center justify-start tabular-nums';

/**
 * The one run identity of the bar: the status, the steps that failed, the
 * dot of a different definition, and the navigation between runs. The run
 * facts show in a row from a bar of 1152px; the facts that the bar has no
 * room for (see factsShownAt), and only those, are in the tooltip of the
 * status, with the facts that the row never shows. A status with nothing to
 * add has no tooltip.
 */
export function RunChip({ fileName, dag }: BarSlotProps) {
  const { ts } = useI18n();
  const run = useWorkspaceRun();
  const dock = useDockCommands();
  const [, setUrl] = useWorkspaceUrl();
  const details = run.details;
  const [chipEl, setChipEl] = useState<HTMLElement | null>(null);
  const barWidth = useBarWidth(chipEl);
  const failedRun = !!run.failedStep;
  const shownFacts = useMemo(
    () => factsShownAt(barWidth, failedRun),
    [barWidth, failedRun]
  );
  const rowShown = shownFacts.size > 0;

  // The history loads only when an arrow is used for the first time. The
  // arrows go through the runs that the run timeline shows (test runs
  // included when it shows them), in its order.
  const [historyOn, setHistoryOn] = useState(false);
  const [pending, setPending] = useState<Direction | null>(null);
  const timeline = useVisibleRuns({ enabled: historyOn });
  const historyReady = historyOn && !timeline.isLoading;
  const olderId = historyReady ? timeline.adjacentId('older') : null;
  const newerId = historyReady ? timeline.adjacentId('newer') : null;
  const { select } = run;

  useEffect(() => {
    if (pending === null || !historyReady) return;
    setPending(null);
    const target = pending === 'older' ? olderId : newerId;
    if (target !== null) select(target);
  }, [pending, historyReady, olderId, newerId, select]);

  const go = (dir: Direction) => {
    if (historyReady) {
      const target = dir === 'older' ? olderId : newerId;
      if (target !== null) select(target);
      return;
    }
    setHistoryOn(true);
    setPending(dir);
  };

  const pinned = run.selection !== 'latest';

  if (!details) {
    return (
      <div
        ref={setChipEl}
        data-testid="ww-run-chip"
        role="status"
        className="flex items-center gap-1.5 text-xs text-muted-foreground"
      >
        <Spinner />
        {ts('Loading run...')}
      </div>
    );
  }

  const olderDisabled = historyReady ? olderId === null : !run.hasRun;
  // The latest run can have a newer test run in the timeline: the history
  // decides, as it does for the older run.
  const newerDisabled = historyReady ? newerId === null : !run.hasRun;

  const failedNode = run.failedStep
    ? run.nodeByStep.get(run.failedStep)
    : undefined;
  // Why the run failed: its own error, else the error of the failed step.
  const failedReason =
    details.error?.trim() || failedNode?.error || failedNode?.rejectionReason;
  // The label is cut to fit the bar: the title has the whole text.
  const failedTitle = run.failedStep
    ? [
        ts(
          failedNode?.status === NodeStatus.Rejected
            ? 'Rejected at {step}'
            : 'Failed at {step}',
          { step: run.failedStep }
        ),
        failedReason,
      ]
        .filter(Boolean)
        .join(': ')
    : undefined;
  // A partial success is amber in the run header and the list: so is the step.
  const failedTone = toneClasses(
    details.status === Status.PartialSuccess ? 'warning' : 'error'
  );
  const openFailedStep = (step: string) => {
    setUrl({ step }, { replace: true });
    dock.openStep({ step, stream: 'stderr' });
  };

  const display = runDisplayStatus(details);
  const visible = visibleFacts(shownFacts, failedRun);
  // The warning dot of a run of another definition stands in front of the run
  // ID, in place of the #. Without the ID in the row it keeps its own slot.
  const markInFacts = rowShown && visible.has('id');
  const hasExtras = hasHiddenFacts(details, visible);
  // The one continuous step of a healthy run: its repeat count takes the
  // place of the progress, which is "0/1" for a step that never ends.
  const continuous = display.healthy
    ? (details.nodes ?? []).find((n) => {
        const policy = readRepeatPolicy(n.step.repeatPolicy);
        return policy !== null && policy.limit === undefined;
      })
    : undefined;
  const continuousPolicy = continuous
    ? readRepeatPolicy(continuous.step.repeatPolicy)
    : null;

  // The skipped steps are not in the count: they did not have to run.
  const nodes = (details.nodes ?? []).filter(
    (n) => n.status !== NodeStatus.Skipped
  );
  const succeeded = nodes.filter((n) => n.status === NodeStatus.Success).length;
  const progressText = ts('{done} of {total} done', {
    done: succeeded,
    total: nodes.length,
  });

  const latestRef = run.latestRef;
  const newerActive =
    pinned &&
    latestRef !== null &&
    ACTIVE_STATUSES.has(latestRef.status) &&
    isNewerRun(latestRef, details);

  return (
    <div
      ref={setChipEl}
      data-testid="ww-run-chip"
      data-status={details.status}
      role="group"
      aria-label={ts(run.isLatest ? 'Last run' : 'Selected run')}
      className="flex min-w-0 items-center gap-2 text-xs"
    >
      <div className="flex shrink-0 items-center">
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={ts('Older run')}
          title={ts(
            olderDisabled && historyReady
              ? 'No older run in this view'
              : 'Older run'
          )}
          disabled={olderDisabled}
          onClick={() => go('older')}
        >
          <ChevronLeft aria-hidden="true" className="size-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={ts('Newer run')}
          title={ts(
            newerDisabled && historyReady
              ? 'No newer run in this view'
              : 'Newer run'
          )}
          disabled={newerDisabled}
          onClick={() => go('newer')}
        >
          <ChevronRight aria-hidden="true" className="size-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          data-testid="ww-run-latest"
          aria-label={ts('Latest run')}
          title={ts(run.isLatest ? 'Following the latest run' : 'Latest run')}
          disabled={run.isLatest && !isEphemeralRun(details)}
          onClick={() => select('latest')}
        >
          <ChevronLast aria-hidden="true" className="size-4" />
        </Button>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {hasExtras ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <span
                tabIndex={0}
                data-testid="ww-run-chip-identity"
                className="inline-flex rounded-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              >
                <StatusIcon
                  status={details.status}
                  healthy={display.healthy}
                  title={false}
                />
              </span>
            </TooltipTrigger>
            <TooltipContent
              side="bottom"
              align="start"
              className="max-w-md text-left"
            >
              <div className="flex flex-col gap-2">
                <div className="font-medium">{ts(display.text)}</div>
                <RunFacts
                  dagRun={details}
                  layout="stacked"
                  shown={shownFacts}
                  failed={failedRun}
                />
              </div>
            </TooltipContent>
          </Tooltip>
        ) : (
          <span
            data-testid="ww-run-chip-identity"
            className="inline-flex rounded-sm"
          >
            <StatusIcon status={details.status} healthy={display.healthy} />
          </span>
        )}
        <span role="status" aria-live="polite" className="sr-only">
          {ts(display.text)}
        </span>
        <AutoRetryBadge
          status={details.status}
          count={details.autoRetryCount}
          limit={details.autoRetryLimit}
        />
      </div>
      {/* The bar never gives the chip less than its smallest width, and the
          full step name counts in it: a cap keeps a long name from pushing
          the run controls out of the bar. */}
      {run.failedStep && (
        <button
          type="button"
          className={cn(
            'inline-flex min-w-0 max-w-56 shrink items-center gap-1 rounded-md px-1.5 py-0.5 font-medium hover:bg-muted',
            // With the row, the failed step stands where the trigger would.
            rowShown && 'order-1',
            failedTone.text
          )}
          title={failedTitle}
          onClick={() => openFailedStep(run.failedStep!)}
        >
          <AlertTriangle aria-hidden="true" className="size-3.5 shrink-0" />
          <span className="truncate">
            {ts(
              failedNode?.status === NodeStatus.Rejected
                ? 'Rejected at {step}'
                : 'Failed at {step}',
              { step: run.failedStep }
            )}
          </span>
        </button>
      )}
      {/* The row of facts shows where the bar has room. Its facts are as wide
          as their text, and it comes after the failed step, which takes the
          place of the trigger. */}
      {rowShown && (
        <div data-testid="ww-run-facts" className="flex shrink-0 order-1">
          <RunFacts
            dagRun={details}
            layout="inline"
            shown={shownFacts}
            failed={failedRun}
            idMark={
              markInFacts ? (
                <RunIdMark fileName={fileName} dag={dag} />
              ) : undefined
            }
          />
        </div>
      )}
      {/* The slot of the count: while a run is active it shows the steps
          done; a run that never ends shows how many times its continuous
          step ran. The slot is there, empty, at any other time, so that the
          chip keeps its width when a run starts or ends. */}
      <span data-testid="ww-run-count-slot" className={COUNT_SLOT}>
        {run.isActive && display.healthy
          ? continuousPolicy &&
            continuous &&
            continuous.doneCount > 0 && (
              <RepeatBadge
                policy={continuousPolicy}
                count={continuous.doneCount}
                running
                compact
                className="shrink-0"
              />
            )
          : nodes.length > 0 && (
              <span
                data-testid="ww-run-progress"
                role="group"
                className={cn(
                  'w-full shrink-0 tabular-nums',
                  run.isActive
                    ? 'text-muted-foreground'
                    : readableToneText(display.tone)
                )}
                aria-label={ts('Run progress')}
              >
                {/* The slash is the centre of the slot: each number stands in
                    the middle of its half, so that 0/2 and 12/12 line up. */}
                <span
                  aria-hidden="true"
                  className="grid grid-cols-[1fr_auto_1fr] items-center"
                >
                  <span className="text-center">{succeeded}</span>
                  <span>/</span>
                  <span className="text-center">{nodes.length}</span>
                </span>
                <span className="sr-only">{progressText}</span>
              </span>
            )}
      </span>
      {isEphemeralRun(details) && (
        <Button
          size="xs"
          variant="ghost"
          data-testid="ww-run-test-chip"
          title={ts('Test run, not kept. Back to latest')}
          onClick={() => select('latest')}
        >
          <FlaskConical aria-hidden="true" className="size-3.5" />
          {ts('Test run')}
        </Button>
      )}
      {/* Without the run ID in the row, the slot of the stale dot is there,
          empty, so that the chip keeps its width when the dot appears. */}
      {!markInFacts && (
        <RunStaleDot
          fileName={fileName}
          dag={dag}
          className={rowShown ? 'order-1' : undefined}
        />
      )}
      {newerActive && (
        <button
          type="button"
          className={cn(
            'shrink-0 rounded-md px-1.5 py-0.5 font-medium text-primary hover:bg-muted',
            rowShown && 'order-2'
          )}
          onClick={() => select('latest')}
        >
          {ts('A newer run is active')}
        </button>
      )}
    </div>
  );
}
