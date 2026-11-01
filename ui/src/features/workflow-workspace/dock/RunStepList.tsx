// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { GitBranch } from 'lucide-react';
import {
  memo,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  type KeyboardEvent,
} from 'react';
import { Link } from 'react-router-dom';
import { NodeStatus } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import RelativeTime from '@/components/ui/relative-time';
import { RepeatBadge } from '@/components/ui/repeat-badge';
import { readRepeatPolicy } from '@/components/ui/repeat-summary';
import { RetryCount } from '@/components/ui/retry-count';
import { StatusDot } from '@/components/ui/status-dot';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { orderNodesForDisplay } from '@/features/dags/components/visualization/timelineItems';
import {
  reusedDurationMs,
  runInputProvenance,
  type InputProvenance,
} from '@/features/visual-editor/run/inputProvenance';
import { ELAPSED_SLOT, useStepElapsed } from '@/hooks/useStepElapsed';
import { formatStepSpan } from '@/lib/formatStepSpan';
import { shortRunId } from '@/lib/shortRunId';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { runDisplayStatus } from '@/lib/runDisplayStatus';
import { nodeStatusText } from '@/lib/status-utils';
import { cn } from '@/lib/utils';
import type { ApiDAGDetails, ApiDAGRunDetails, ApiNode } from '../contracts';
import type { RunHandler } from '../data';
import { sameContent } from '../data/sameContent';
import { useDockHidden } from './FreezeWhenHidden';
import { propsEqual } from './propsEqual';
import { stepReuse, type StepReuse } from './stepReuse';

// The Run panel narrower than this shows the rows as status dots; the text
// stays for screen readers and the keyboard. The container query reads the
// nearest @container: the body of the Run panel.
const ICONS_ONLY = '@max-[900px]:sr-only';
// ICONS_ONLY hides the focus ring of a row's button: the row shows it.
const ICONS_ONLY_FOCUS =
  '@max-[900px]:has-[:focus-visible]:ring-1 @max-[900px]:has-[:focus-visible]:ring-inset @max-[900px]:has-[:focus-visible]:ring-ring';

export interface RunStepListProps {
  dag: ApiDAGDetails;
  details: ApiDAGRunDetails;
  handlers: readonly RunHandler[];
  /** Server step name of the selected row; null selects the summary row. */
  selected: string | null;
  onSelect(step: string | null): void;
}

/** Step and handler names of the saved definition. */
function definitionNames(dag: ApiDAGDetails): Set<string> | null {
  if (!dag.steps) return null;
  const names = new Set(dag.steps.map((step) => step.name));
  const handlerOn = dag.handlerOn as
    | Record<string, { name?: string } | undefined>
    | undefined;
  for (const handler of Object.values(handlerOn ?? {})) {
    if (handler?.name) names.add(handler.name);
  }
  return names;
}

/**
 * The duration of the step on the row: the text of the canvas card, live
 * while the step runs, in a slot of fixed width. A skipped step did not run:
 * it has a Skipped label in place of a duration. The start time is a tooltip
 * and a screen reader text, because "3h ago" on every row says nothing.
 */
function StepTiming({
  node,
  reused,
}: {
  node: ApiNode;
  /** Where the outputs of the step come from, when the run reused it. */
  reused?: InputProvenance;
}) {
  const { ts } = useI18n();
  const hidden = useDockHidden();
  const duration = useStepElapsed(node, !hidden);
  // A step that the run reused took its time in another run: the row says
  // "Reused" and shows that time, with the run in the tooltip.
  const original = reused ? reusedDurationMs(reused) : null;
  if (node.status === NodeStatus.Skipped && reused) {
    return (
      <span
        data-testid="run-step-reused"
        title={
          reused.runId
            ? ts('Took this long in run {id}', {
                id: shortRunId(reused.runId),
              })
            : undefined
        }
        className="flex shrink-0 items-center gap-1 text-[11px] leading-4 text-muted-foreground"
      >
        {ts('Reused')}
        {original !== null && (
          <span
            data-testid="run-step-duration"
            className={cn(ELAPSED_SLOT, 'text-right')}
          >
            {formatStepSpan(
              new Date(0).toISOString(),
              new Date(original).toISOString(),
              { belowSecond: ts('<1s'), wholeSecond: true }
            )}
          </span>
        )}
      </span>
    );
  }
  // A skipped step has no duration; the label tells it from one not run yet.
  if (node.status === NodeStatus.Skipped) {
    return (
      <span
        data-testid="run-step-skipped"
        className="text-[11px] leading-4 text-muted-foreground"
      >
        {ts('Skipped')}
      </span>
    );
  }
  // An aborted step, such as one behind a failed step, says so in words; the
  // error of the node (for example 'upstream failed') is the tooltip.
  const aborted =
    node.status === NodeStatus.Aborted ? (
      <span
        data-testid="run-step-aborted"
        title={node.error || undefined}
        className="mr-1 text-[11px] leading-4 text-muted-foreground"
      >
        {ts(nodeStatusText(node.status))}
      </span>
    ) : null;
  if (!node.startedAt || node.startedAt === '-') return aborted;
  return (
    <span
      title={formatAbsoluteTime(node.startedAt) ?? undefined}
      className="flex shrink-0 items-center text-[11px] leading-4 text-muted-foreground"
    >
      {aborted}
      {duration !== null && (
        <span
          data-testid="run-step-duration"
          className={cn(ELAPSED_SLOT, 'text-right')}
        >
          {duration}
        </span>
      )}
      <RelativeTime
        compact
        ago
        timestamp={node.startedAt}
        className="sr-only"
      />
    </span>
  );
}

/** The build decision, with the reason, the detail and the producer run. */
function BuildBadge({ build }: { build: NonNullable<ApiNode['build']> }) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const producer = build.producerRun;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge
          tabIndex={0}
          size="sm"
          variant={build.decision === 'reuse' ? 'success' : 'default'}
          data-testid="run-step-build"
          className="focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          {build.decision === 'reuse'
            ? ts('reused')
            : build.decision === 'always'
              ? ts('always run')
              : build.decision}
        </Badge>
      </TooltipTrigger>
      <TooltipContent className="max-w-80 space-y-1 text-xs">
        <div className="font-medium">{build.reason.replace(/_/g, ' ')}</div>
        {build.decision === 'reuse' && <div>{ts('No executor ran.')}</div>}
        {build.detail && <div>{build.detail}</div>}
        {producer?.name && producer.id && (
          <Link
            className="block underline underline-offset-2"
            to={buildDAGRunPageURL({
              rootDAGRunName: producer.name,
              rootDAGRunId: producer.id,
              remoteNode,
            })}
            onClick={(event) => event.stopPropagation()}
          >
            {ts('Produced by')} {producer.name}:{producer.id}
          </Link>
        )}
      </TooltipContent>
    </Tooltip>
  );
}

interface StepRowProps {
  node: ApiNode;
  event?: string;
  reused?: InputProvenance;
  /** The step was reused: its dot is hatched, green or red. */
  reuse?: StepReuse;
  selected: boolean;
  outside: boolean;
  rowRef(name: string, el: HTMLLIElement | null): void;
  onSelect(name: string): void;
}

/** The node is compared by content: a new copy of the same step is no change. */
function rowEqual(prev: StepRowProps, next: StepRowProps): boolean {
  return (
    sameContent(prev.node, next.node) &&
    propsEqual({ ...prev, node: null }, { ...next, node: null })
  );
}

const StepRow = memo(function StepRow({
  node,
  event,
  reused,
  reuse,
  selected,
  outside,
  rowRef,
  onSelect,
}: StepRowProps) {
  const { ts } = useI18n();
  const step = node.step;
  const subRuns = [...(node.subRuns ?? []), ...(node.subRunsRepeated ?? [])];
  const subDagName = step.call || subRuns[0]?.dagName;
  const hasSubRuns = !!subDagName && subRuns.length > 0;
  const repeat = useMemo(
    () => readRepeatPolicy(step.repeatPolicy),
    [step.repeatPolicy]
  );
  const build = node.build;

  return (
    <li
      ref={(el) => rowRef(step.name, el)}
      data-step={step.name}
      aria-current={selected ? 'true' : undefined}
      data-selected={selected ? 'true' : undefined}
      title={step.name}
      onClick={() => onSelect(step.name)}
      className={cn(
        'flex min-h-6 cursor-pointer items-start gap-2 px-3 py-1 text-xs leading-4 [contain:layout_paint] hover:bg-muted/60 @max-[900px]:justify-center @max-[900px]:px-0',
        ICONS_ONLY_FOCUS,
        selected && 'bg-primary/10 hover:bg-primary/15'
      )}
    >
      <StatusDot
        kind="step"
        status={node.status}
        reused={reuse?.outcome}
        reusedFrom={reuse?.runId ? shortRunId(reuse.runId) : undefined}
        className="mt-1"
      />
      <div className={cn('min-w-0 flex-1', ICONS_ONLY)}>
        <div className="flex min-w-0 items-center gap-1.5">
          <button
            type="button"
            className="min-w-0 truncate text-left text-xs font-medium leading-4 text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          >
            {step.name}
          </button>
          {event && event !== step.name && (
            <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
              {event}
            </span>
          )}
          {hasSubRuns && (
            <Tooltip>
              <TooltipTrigger asChild>
                <span
                  tabIndex={0}
                  data-testid="run-step-subdag"
                  className="inline-flex shrink-0 items-center gap-0.5 rounded-sm text-primary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                >
                  <GitBranch className="h-3.5 w-3.5" aria-hidden="true" />
                  <span
                    className="text-[10px] tabular-nums"
                    title={ts('{count} sub-runs', { count: subRuns.length })}
                  >
                    {subRuns.length}
                  </span>
                </span>
              </TooltipTrigger>
              <TooltipContent>
                <span className="text-xs">
                  {ts('Sub DAG Run:')} {subDagName}
                </span>
              </TooltipContent>
            </Tooltip>
          )}
        </div>
        {step.description && (
          <div
            className="truncate text-xs text-muted-foreground"
            title={step.description}
          >
            {step.description}
          </div>
        )}
        {(outside || node.retryCount > 0 || repeat || build) && (
          <div className="mt-0.5 flex flex-wrap items-center gap-1">
            {node.retryCount > 0 && <RetryCount count={node.retryCount} />}
            {repeat && (
              <RepeatBadge
                policy={repeat}
                count={node.doneCount}
                running={node.status === NodeStatus.Running}
              />
            )}
            {build && <BuildBadge build={build} />}
            {outside && (
              <span className="text-[10px] italic text-muted-foreground">
                {ts('not in current definition')}
              </span>
            )}
          </div>
        )}
      </div>
      <span className={cn('self-start', ICONS_ONLY)}>
        <StepTiming node={node} reused={reused} />
      </span>
    </li>
  );
}, rowEqual);

/**
 * The rows of the selected run: the summary row, the steps in display order, the
 * lifecycle handlers, each run node that the saved definition no longer has
 * carrying a marker.
 */
export function RunStepList({
  dag,
  details,
  handlers,
  selected,
  onSelect,
}: RunStepListProps) {
  const { ts } = useI18n();
  const known = useMemo(() => definitionNames(dag), [dag]);
  // By drawn start and dependency order: a step never sits above one that it
  // waits for, also when the times of both are the same second (a run that
  // an older version stored).
  const nodes = useMemo(
    () => orderNodesForDisplay(details, Date.now()),
    [details]
  );
  const reused = useMemo(() => runInputProvenance(details), [details]);
  const reuse = useMemo(() => stepReuse(details), [details]);
  const rows = useRef(new Map<string, HTMLLIElement>());
  const listRef = useRef<HTMLUListElement>(null);
  const summaryRef = useRef<HTMLLIElement>(null);
  const rowRef = useCallback((name: string, el: HTMLLIElement | null) => {
    if (el) rows.current.set(name, el);
    else rows.current.delete(name);
  }, []);
  // Stable, so that the memoized rows skip the render of a new selection.
  const onSelectRef = useRef(onSelect);
  onSelectRef.current = onSelect;
  const selectRow = useCallback(
    (name: string) => onSelectRef.current(name),
    []
  );

  // Also for a selection made on the canvas, so the row comes into view. A
  // key that moves the selection moves the focus with it.
  useEffect(() => {
    const row =
      selected === null ? summaryRef.current : rows.current.get(selected);
    if (selected !== null) row?.scrollIntoView?.({ block: 'nearest' });
    const list = listRef.current;
    if (list?.contains(document.activeElement)) {
      row?.querySelector<HTMLElement>('button')?.focus();
    }
  }, [selected]);

  // The summary row, the steps in display order, then the handlers.
  const order = [
    null,
    ...nodes.map((node) => node.step.name),
    ...handlers.map(({ node }) => node.step.name),
  ];
  const onKeyDown = (event: KeyboardEvent<HTMLUListElement>) => {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) {
      return;
    }
    const at = order.indexOf(selected);
    const to = {
      ArrowUp: at - 1,
      ArrowDown: at + 1,
      Home: 0,
      End: order.length - 1,
    }[event.key];
    if (to === undefined) return;
    event.preventDefault();
    const next = order[Math.min(order.length - 1, Math.max(0, to))];
    if (next !== undefined && next !== selected) onSelect(next);
  };

  const outside = (node: ApiNode) =>
    known !== null && !known.has(node.step.name);

  return (
    <ul
      ref={listRef}
      aria-label={ts('Run steps')}
      onKeyDown={onKeyDown}
      className="py-1"
    >
      <li
        ref={summaryRef}
        data-summary=""
        aria-current={selected === null ? 'true' : undefined}
        data-selected={selected === null ? 'true' : undefined}
        title={ts('Run summary')}
        onClick={() => onSelect(null)}
        className={cn(
          'flex min-h-6 cursor-pointer items-center gap-2 px-3 py-1 hover:bg-muted/60 @max-[900px]:justify-center @max-[900px]:px-0',
          ICONS_ONLY_FOCUS,
          selected === null && 'bg-primary/10 hover:bg-primary/15'
        )}
      >
        <StatusDot
          kind="run"
          status={details.status}
          healthy={runDisplayStatus(details).healthy}
          // The header chip of the run pulses: one pulse for the run.
          className="motion-safe:animate-none"
        />
        <button
          type="button"
          className={cn(
            'min-w-0 flex-1 truncate text-left text-xs font-medium leading-4 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring',
            ICONS_ONLY
          )}
        >
          {ts('Run summary')}
        </button>
      </li>
      {nodes.map((node, i) => (
        <StepRow
          key={`step:${i}:${node.step.name}`}
          node={node}
          reused={reused.get(node.step.name)}
          reuse={reuse.get(node.step.name)}
          selected={selected === node.step.name}
          outside={outside(node)}
          rowRef={rowRef}
          onSelect={selectRow}
        />
      ))}
      {handlers.length > 0 && (
        <>
          <li
            role="presentation"
            className="px-3 pb-0.5 pt-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground @max-[900px]:sr-only"
          >
            {ts('Lifecycle handlers')}
          </li>
          {handlers.map(({ event, node }) => (
            <StepRow
              key={`handler:${event}`}
              node={node}
              event={event}
              selected={selected === node.step.name}
              outside={outside(node)}
              rowRef={rowRef}
              onSelect={selectRow}
            />
          ))}
        </>
      )}
    </ul>
  );
}
