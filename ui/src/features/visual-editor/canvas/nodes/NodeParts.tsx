// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Check,
  Copy,
  CopyPlus,
  PanelRightOpen,
  Play,
  Trash2,
} from 'lucide-react';
import {
  useEffect,
  useRef,
  useState,
  type ComponentProps,
  type CSSProperties,
  type ReactElement,
  type ReactNode,
} from 'react';
import { NodeStatus } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { IconButton } from '@/components/ui/icon-button';
import { RepeatBadge } from '@/components/ui/repeat-badge';
import { RetryCount } from '@/components/ui/retry-count';
import { StatusDot } from '@/components/ui/status-dot';
import { useI18n } from '@/i18n/I18nProvider';
import { ELAPSED_SLOT, useStepElapsed } from '@/hooks/useStepElapsed';
import { formatStepSpan } from '@/lib/formatStepSpan';
import { shortRunId } from '@/lib/shortRunId';
import { statusLabelText } from '@/lib/status-utils';
import { cn } from '@/lib/utils';
import type { CanvasDirection, CanvasNode, StepKey } from '../../contracts';
import { plainIssueMessage } from '../../inspector/issues';
import { InputMark } from '../../references/InputMark';
import { reusedDurationMs } from '../../run/inputProvenance';
import { CountBadge } from '../CountBadge';
import { useCanvasActions } from '../context';
import { DASHED } from '../edges/dash';
import { Position } from '../engine';
import { actionIcon } from '../icons';
import { statusBorderClass } from '../statusStyle';
import {
  COMPACT_CAPTION_HEIGHT,
  COMPACT_CAPTION_WIDTH,
  COMPACT_NODE_SIZE,
  compactBox,
  compactCardOffset,
} from '../layout';
import { canvasShortcut } from '../useCanvasKeymap';
import { useToolbarPlacement } from './toolbarPlacement';

/** Side of a node where edges leave (`source`) or arrive (`target`). */
export function handlePosition(
  direction: CanvasDirection,
  side: 'source' | 'target'
): Position {
  if (direction === 'TB') {
    return side === 'source' ? Position.Bottom : Position.Top;
  }
  return side === 'source' ? Position.Right : Position.Left;
}

export const HANDLE_CLASS =
  '!h-2 !w-2 !rounded-full !border !border-border !bg-muted-foreground';

/**
 * Outline of a node card. In the light theme it is slightly thicker and
 * darker; the dark theme keeps its 1 px outline.
 */
export const CARD_OUTLINE =
  'border-[1.5px] border-muted-foreground/60 dark:border dark:border-muted-foreground/40';

/**
 * The light of the cursor of the YAML, and the same light while a host
 * spotlights the node (`data-spotlit` on its box, see VisualCanvas).
 */
const HIGHLIGHT = 'ring-2 ring-primary/50 shadow-lg shadow-primary/30';
const SPOTLIT =
  'group-data-[spotlit]/node:ring-2 group-data-[spotlit]/node:ring-primary/50 group-data-[spotlit]/node:shadow-lg group-data-[spotlit]/node:shadow-primary/30';

/**
 * State classes of a node card: the selection ring wins over the light of the
 * cursor of the YAML or of a host, and that wins over the colour of the run
 * status, which is the border of the card itself. The card of a previous step
 * that the run reused (see `reusedOutline`) has no selection ring and no
 * border colour: its dashed outline is its only line, except while the cursor
 * of the YAML or a host lights it.
 */
export function cardStateClass(
  node: Pick<CanvasNode, 'run' | 'reuseOutline'>,
  selected: boolean | undefined,
  highlighted?: boolean
): string | undefined {
  if (node.reuseOutline && node.run) {
    // The dashed outline is the one line of a reused card at rest, on hover
    // or selected (its dashes turn violet); only the light adds a ring.
    if (selected) return undefined;
    return highlighted ? HIGHLIGHT : SPOTLIT;
  }
  const border = node.run ? statusBorderClass(node.run.status) : undefined;
  if (selected) return cn('ring-2 ring-primary', border);
  return cn(highlighted ? HIGHLIGHT : SPOTLIT, border);
}

/**
 * The border class of the card of a previous step that the shown run reused.
 * The card keeps the width of its border, so that the box does not move, but
 * the line is transparent: the dashes are drawn by `ReusedOutline`, with the
 * dash pattern of a launcher that is not set up. The border stays
 * transparent on hover, focus and selection. Undefined for a step that is not
 * a previous step of an executed step (see `reuseOutline` of the node).
 */
export function reusedOutline(
  node: Pick<CanvasNode, 'reuseOutline'>
): string | undefined {
  if (!node.reuseOutline) {
    return undefined;
  }
  return 'border-transparent hover:border-transparent focus-within:border-transparent dark:border-transparent dark:hover:border-transparent dark:focus-within:border-transparent';
}

/**
 * The dashed outline of a reused card: an SVG rectangle with the dash
 * pattern of the edges and of the launchers that are not set up, in the
 * success colour when the source run gave the outputs of the step, in the
 * destructive colour when it did not, and in the primary colour while the card
 * is selected. A state never changes its dashes, only their colour.
 */
export function ReusedOutline({
  tone,
  selected = false,
}: {
  tone: 'success' | 'failure';
  /** The card is selected: its dashes are violet, whatever the tone. */
  selected?: boolean;
}): ReactElement {
  return (
    <svg
      aria-hidden="true"
      data-testid="ve-node-reuse-outline"
      className="pointer-events-none absolute -inset-px h-[calc(100%+2px)] w-[calc(100%+2px)] overflow-visible"
    >
      <rect
        x={0.75}
        y={0.75}
        rx={6}
        ry={6}
        fill="none"
        strokeWidth={1.5}
        strokeDasharray={DASHED}
        style={{ width: 'calc(100% - 1.5px)', height: 'calc(100% - 1.5px)' }}
        className={
          selected
            ? 'stroke-primary'
            : tone === 'success'
              ? 'stroke-success'
              : 'stroke-destructive'
        }
      />
    </svg>
  );
}

/**
 * The tooltip line of a card with a reuse outline: the source run and what
 * the source gave. Undefined for any other card.
 */
export function reuseTitle(
  node: Pick<CanvasNode, 'reuseOutline' | 'run'>,
  ts: ReturnType<typeof useI18n>['ts']
): string | undefined {
  const run = node.run;
  if (node.reuseOutline === 'success' && run?.reused?.runId) {
    return ts('Reused from run {id}: succeeded', {
      id: shortRunId(run.reused.runId),
    });
  }
  if (node.reuseOutline === 'failure' && run?.reuseFailed) {
    return ts('Reused from run {id}: failed', {
      id: shortRunId(run.reuseFailed.runId),
    });
  }
  return undefined;
}

/** The reason of a failed step, for the tooltip of its card; else undefined. */
export function failureTitle(run: CanvasNode['run']): string | undefined {
  if (run?.status !== NodeStatus.Failed) return undefined;
  return run.error?.trim() || undefined;
}

/** Icon tile of a node. */
export function NodeIcon({
  node,
  small = false,
}: {
  node: Pick<CanvasNode, 'kind' | 'action'>;
  small?: boolean;
}) {
  const Icon = actionIcon(node);
  return (
    <span
      className={cn(
        'flex shrink-0 items-center justify-center rounded bg-muted text-muted-foreground',
        small ? 'h-6 w-6' : 'h-8 w-8'
      )}
    >
      <Icon className={small ? 'h-3.5 w-3.5' : 'h-4 w-4'} aria-hidden="true" />
    </span>
  );
}

/** Count label followed by the messages, one per line. */
function issueTitle(count: string, messages: string[] | undefined): string {
  return messages && messages.length > 0
    ? [count, ...messages.map(plainIssueMessage)].join('\n')
    : count;
}

/**
 * Error and warning counts of a node, as the count badge of the Cron
 * launcher: the error tone for the errors, the warning tone for the warnings.
 * The tooltip of each count lists the messages. Shows nothing when both counts are zero. With `corner`, the
 * counts sit on the top right corner of the card, over its border, as the
 * count of the Cron launcher does: the errors on the corner and the warnings
 * on their left. The card must be positioned.
 */
export function IssueBadges({
  issues,
  issueMessages,
  corner = false,
}: Pick<CanvasNode, 'issues' | 'issueMessages'> & { corner?: boolean }) {
  const { ts } = useI18n();
  const { errors, warnings } = issues;
  if (errors === 0 && warnings === 0) {
    return null;
  }
  const errorTitle = issueTitle(
    ts('Errors: {count}', { count: errors }),
    issueMessages?.errors
  );
  const warningTitle = issueTitle(
    ts('Warnings: {count}', { count: warnings }),
    issueMessages?.warnings
  );
  const badges = (
    <>
      {errors > 0 && (
        <CountBadge
          role="img"
          aria-label={errorTitle}
          title={errorTitle}
          tone="error"
          count={errors}
        />
      )}
      {warnings > 0 && (
        <CountBadge
          role="img"
          aria-label={warningTitle}
          title={warningTitle}
          tone="warning"
          count={warnings}
        />
      )}
    </>
  );
  return corner ? (
    <span
      data-testid="ve-canvas-node-issues"
      className="absolute -right-2 -top-2 z-10 flex flex-row-reverse gap-1"
    >
      {badges}
    </span>
  ) : (
    badges
  );
}

/**
 * The text that a host put in `statusLabel` for the dot, such as the retry
 * preview does, or undefined for a raw API label such as 'succeeded': the dot
 * then names the translated status.
 */
function hostStatusLabel(run: NonNullable<CanvasNode['run']>) {
  const label = run.statusLabel;
  return label && statusLabelText(label) === label ? label : undefined;
}

/**
 * Status dot of the run of a node. Its name is the translated status, or the
 * text of the host. A step that waits for an approval or an input also gets a
 * Waiting badge.
 */
export function RunDot({
  run,
  reuseOutline,
  dotOnly = false,
}: Pick<CanvasNode, 'run' | 'reuseOutline'> & {
  /** No Waiting badge: a compact card has no room for it. */
  dotOnly?: boolean;
}) {
  const { ts } = useI18n();
  if (!run) {
    return null;
  }
  // Only a step that an executed step depends on is a step that the source
  // run could not give: a step after it is skipped and shows a plain dot.
  const reuse: 'success' | 'failed' | undefined = run.reused
    ? 'success'
    : reuseOutline === 'failure' && run.reuseFailed
      ? 'failed'
      : undefined;
  const sourceRunId = run.reused?.runId ?? run.reuseFailed?.runId ?? '';
  const waitingLabel = ts('Waiting');
  return (
    <>
      <span
        data-testid="ve-canvas-node-status"
        data-status={run.status}
        className="inline-flex shrink-0"
      >
        {/* A step that the run reused has the hatched dot of the Activity
            list: green when its source run gave the outputs, red when not. */}
        <StatusDot
          kind="step"
          status={run.status}
          label={run.reused ? ts('Reused') : hostStatusLabel(run)}
          reused={reuse}
          reusedFrom={reuse ? shortRunId(sourceRunId) : undefined}
        />
      </span>
      {!dotOnly && run.status === NodeStatus.Waiting && (
        <Badge
          variant="warning"
          size="sm"
          role="img"
          aria-label={waitingLabel}
          title={waitingLabel}
          data-testid="ve-canvas-node-waiting"
        >
          {waitingLabel}
        </Badge>
      )}
    </>
  );
}

/**
 * Duration and retry count of the run of a node. The duration is the text of
 * the run list (useStepElapsed): it counts up each second while the step
 * runs, in a slot of fixed width so that the card keeps its size.
 */
export function RunMeta({ run }: Pick<CanvasNode, 'run'>) {
  const { ts } = useI18n();
  const elapsed = useStepElapsed(run);
  if (!run) {
    return null;
  }
  // A step that did not start, or was skipped, can carry equal dates: no
  // '0ms' for it. A step that the run reused shows the time that it took in
  // its source run.
  const original = run.reused ? reusedDurationMs(run.reused) : null;
  const duration =
    run.status === NodeStatus.NotStarted
      ? null
      : (elapsed ??
        (original === null
          ? null
          : formatStepSpan(
              new Date(0).toISOString(),
              new Date(original).toISOString(),
              { belowSecond: ts('<1s') }
            )));
  if (!duration && run.retryCount <= 0) {
    return null;
  }
  return (
    <span className="flex shrink-0 items-center gap-1.5 text-[10px] text-muted-foreground">
      {run.retryCount > 0 && <RetryCount count={run.retryCount} />}
      {duration && (
        <span
          data-testid="ve-node-duration"
          data-reused={elapsed === null && original !== null ? '' : undefined}
          title={
            elapsed === null && original !== null
              ? ts('Took this long in run {id}', {
                  id: shortRunId(run.reused?.runId ?? ''),
                })
              : undefined
          }
          className={cn(ELAPSED_SLOT, 'text-right')}
        >
          {duration}
        </span>
      )}
    </span>
  );
}

/**
 * The repeat marker of a step that has a `repeat_policy`: the shared
 * RepeatBadge, with the number of runs of the followed run and a pulse while
 * the step runs. Nothing for a step that does not repeat.
 */
export function RepeatMarker({
  node,
  compact = false,
  className,
}: {
  node: Pick<CanvasNode, 'repeat' | 'run'>;
  compact?: boolean;
  className?: string;
}) {
  if (!node.repeat) {
    return null;
  }
  return (
    <RepeatBadge
      policy={node.repeat}
      count={node.run?.doneCount}
      running={node.run?.status === NodeStatus.Running}
      compact={compact}
      className={cn('shrink-0', className)}
    />
  );
}

/**
 * The mark of a step that the shown run did not execute but whose outputs it
 * reused. A card that has room shows a text chip ("from run 034mhevD", or
 * "cache of run 034mhevD"); a compact card shows the icon alone. Both have the
 * source run (id, time, link, or "no longer available") in their tooltip. The
 * status of the card stays Skipped; the card is outlined with a dashed line
 * (see `reusedOutline`). Nothing for any other step.
 */
export function ReusedMarker({
  node,
  chip = false,
  className,
}: {
  node: Pick<CanvasNode, 'run'>;
  /** A text chip in place of the icon alone. */
  chip?: boolean;
  className?: string;
}) {
  const reused = node.run?.reused;
  if (!reused) {
    return null;
  }
  return (
    <InputMark
      provenance={reused}
      variant={chip ? 'chip' : 'icon'}
      testId="ve-node-reused"
      className={className}
    />
  );
}

/**
 * Icon, label and subtitle of a node, with run state. The issue badges sit
 * on the top right corner of the card.
 */
export function NodeHeader({
  node,
  className,
}: {
  node: CanvasNode;
  className?: string;
}) {
  return (
    <div className={cn('flex min-w-0 items-center gap-2 px-2.5', className)}>
      <IssueBadges
        corner
        issues={node.issues}
        issueMessages={node.issueMessages}
      />
      <NodeIcon node={node} />
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex min-w-0 items-center gap-1.5">
          <span
            className="min-w-0 flex-1 truncate text-sm font-medium"
            title={node.label}
          >
            {node.label}
          </span>
          <RunDot run={node.run} reuseOutline={node.reuseOutline} />
        </div>
        <div className="flex min-w-0 items-center gap-1.5">
          {/* The chip of a reused step starts the row: before the subtitle,
              which can shrink to nothing, it leaves no gap on its left. */}
          <ReusedMarker node={node} chip />
          {node.subtitle && (
            <span
              className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground"
              title={node.subtitle}
            >
              {node.subtitle}
            </span>
          )}
          <RepeatMarker node={node} />
          <RunMeta run={node.run} />
        </div>
      </div>
    </div>
  );
}

/** Tooltip and name of a compact card: the label and the subtitle, or the failure. */
export function compactTitle(
  node: Pick<CanvasNode, 'label' | 'subtitle' | 'run'>
): string {
  const text = [node.label, node.subtitle].filter(Boolean).join('\n');
  const failure = failureTitle(node.run);
  return failure ? `${text}\n${failure}` : text;
}

/**
 * Icon of a node with its status dot at the top left, its repeat marker at
 * the bottom right, its reuse marker at the bottom left and its issue counts on the top right corner: the body of a compact card, which has no text. The
 * name is the caption under the card.
 */
export function NodeCompactBody({
  node,
  className,
}: {
  node: CanvasNode;
  className?: string;
}) {
  return (
    <div className={cn('flex min-w-0 items-center justify-center', className)}>
      <NodeIcon node={node} />
      {node.run && (
        <span className="absolute left-1 top-1 flex">
          <RunDot run={node.run} reuseOutline={node.reuseOutline} dotOnly />
        </span>
      )}
      {node.repeat && (
        <span className="absolute bottom-0.5 right-0.5 flex">
          <RepeatMarker node={node} compact />
        </span>
      )}
      {node.run?.reused && (
        <span className="absolute bottom-0.5 left-0.5 flex">
          <ReusedMarker node={node} />
        </span>
      )}
      <IssueBadges
        corner
        issues={node.issues}
        issueMessages={node.issueMessages}
      />
    </div>
  );
}

/**
 * The frame of a compact step: its box in the layout, which holds the card
 * and the caption under it, the name of the step or its id. The card, the
 * handles and the add stalk are the children. The handles sit on the middle
 * of the sides of the box, which are on the middle of the card too, except
 * for the lower side of a top-to-bottom box: the link leaves under the
 * caption.
 */
export function CompactFrame({
  direction,
  label,
  children,
}: {
  direction: CanvasDirection;
  label: string;
  children: ReactNode;
}) {
  const box = compactBox(direction);
  const { top } = compactCardOffset(direction);
  return (
    <div
      data-testid="ve-canvas-compact-frame"
      className="relative"
      style={{ width: box.width, height: box.height }}
    >
      {children}
      <span
        data-testid="ve-canvas-node-caption"
        title={label}
        // One line, centred under the card, and cut with an ellipsis. The
        // background keeps a link that passes behind it from crossing the text.
        className="absolute truncate rounded-sm bg-background/80 px-1 text-center text-xs leading-5 text-foreground"
        style={{
          left: (box.width - COMPACT_CAPTION_WIDTH) / 2,
          top: top + COMPACT_NODE_SIZE,
          width: COMPACT_CAPTION_WIDTH,
          height: COMPACT_CAPTION_HEIGHT,
        }}
      >
        {label}
      </span>
    </div>
  );
}

/** Place of a compact card in its frame. */
export function compactCardPlace(direction: CanvasDirection): CSSProperties {
  return { ...compactCardOffset(direction) };
}

/** The Copy button swaps its icon for a Check for this long after a click. */
const COPIED_MS = 2000;

type ToolButtonProps = Pick<
  ComponentProps<typeof IconButton>,
  'tabIndex' | 'tooltipSide' | 'aria-description'
>;

/** Copy step: the icon becomes a Check for 2 s, as every copy button does. */
function CopyStepButton({
  step,
  ...toolProps
}: ToolButtonProps & { step: StepKey }) {
  const { ts } = useI18n();
  const { onCommand } = useCanvasActions();
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  return (
    <IconButton
      {...toolProps}
      size="sm"
      className="nodrag nopan"
      label={copied ? ts('Copied') : ts('Copy step')}
      icon={copied ? Check : Copy}
      iconClassName={copied ? 'text-success' : undefined}
      shortcut={canvasShortcut('copy')}
      onClick={() => {
        onCommand({ kind: 'copy', step });
        setCopied(true);
        clearTimeout(timer.current);
        timer.current = setTimeout(() => setCopied(false), COPIED_MS);
      }}
    />
  );
}

/** Time in ms that the buttons of a toolbar stay mounted after the pointer leaves. */
export const TOOLBAR_LINGER_MS = 2000;

/**
 * Icon buttons above a node, at its right. They show on hover, on focus and
 * when the node is selected, and they are mounted only then. The parent of the toolbar must have the `group`
 * class. The toolbar never sits beside the node: a neighbour would be under
 * it, and Delete could cover the step that the user wants to open. A node in
 * a dense grid (`inside`) carries the toolbar on its own top edge, because
 * the gap above it is too small. The toolbar leaves the centre of the card
 * free: a click there selects the step and never hits a button. A compact
 * card (`centred`) has the toolbar centred over it.
 */
export function NodeToolbar({
  step,
  label,
  selected,
  runnable,
  inside = false,
  centred = false,
}: {
  step: StepKey;
  /** The name of the step: the buttons give it as their description. */
  label?: string;
  selected: boolean | undefined;
  /** False for handlers: they cannot run on their own. A read-only canvas has no Run step either. */
  runnable: boolean;
  inside?: boolean;
  /** Centres the toolbar over the card instead of the right edge. */
  centred?: boolean;
}) {
  const { ts } = useI18n();
  const { editable, readOnly, onCommand, onDeleteStep, stepCommandGates } =
    useCanvasActions();
  // A host that takes the runs says whether a step can run.
  const canRun =
    runnable && !readOnly && (stepCommandGates?.canRunStep ?? true);
  // Only the toolbar of the selected step is a tab stop: the arrow keys
  // select a step, so a graph of 100 steps is not 400 tab stops.
  const tabIndex = selected ? 0 : -1;
  // The buttons mount only for a selected, hovered or focused node. Every
  // node follows each pan and zoom: on a graph of 100 steps, 500 mounted
  // tooltips made a pan step twice as slow.
  const [hot, setHot] = useState(false);
  // The toolbar is on screen for a selected node or a node under the pointer
  // or the focus. Only then does it follow the pan and the zoom.
  const [over, setOver] = useState(false);
  const active = Boolean(selected) || hot;
  const { ref, placement, scale, shiftX } = useToolbarPlacement(
    Boolean(selected) || over,
    centred ? 'center' : 'right'
  );
  useEffect(() => {
    // The node box of the canvas; a card alone, as in a unit test, is its own.
    const node =
      ref.current?.closest<HTMLElement>('[data-flow-node]') ??
      ref.current?.parentElement;
    if (!node) return;
    let linger: ReturnType<typeof setTimeout> | undefined;
    const enter = () => {
      clearTimeout(linger);
      setHot(true);
      setOver(true);
    };
    const leave = (event: Event) => {
      const next = (event as FocusEvent).relatedTarget;
      if (
        event.type === 'focusout' &&
        next instanceof Node &&
        node.contains(next)
      ) {
        return;
      }
      // The buttons are hidden by CSS as soon as the pointer leaves. They stay
      // mounted a while, so that a pointer that comes back mounts nothing.
      setOver(false);
      clearTimeout(linger);
      linger = setTimeout(() => setHot(false), TOOLBAR_LINGER_MS);
    };
    node.addEventListener('pointerenter', enter);
    node.addEventListener('focusin', enter);
    node.addEventListener('pointerleave', leave);
    node.addEventListener('focusout', leave);
    return () => {
      clearTimeout(linger);
      node.removeEventListener('pointerenter', enter);
      node.removeEventListener('focusin', enter);
      node.removeEventListener('pointerleave', leave);
      node.removeEventListener('focusout', leave);
    };
  }, [ref]);
  // The tooltip opens on the side of the toolbar that faces away from the card.
  const toolProps: ToolButtonProps = {
    tabIndex,
    'aria-description': label,
    tooltipSide: inside || !placement.below ? 'top' : 'bottom',
  };
  // The zoom of the canvas scales the node: the toolbar cancels it, from its
  // anchor corner, and moves in screen px to stay inside the canvas.
  const edge = centred ? 'center' : 'right';
  const origin = inside
    ? `${edge} center`
    : placement.below
      ? `${edge} top`
      : `${edge} bottom`;
  return (
    <div
      ref={ref}
      data-testid="ve-canvas-node-toolbar"
      data-side={inside ? undefined : placement.below ? 'below' : 'above'}
      style={{
        transformOrigin: origin,
        transform: `translateX(${shiftX}px) scale(${scale})`,
      }}
      className={cn(
        // No fade and no colour transition: a pointer that crosses the graph
        // shows and hides the toolbar of each node, and every frame of a
        // transition is a style recalculation.
        'nodrag nopan absolute z-20 [&_button]:transition-none',
        inside
          ? 'right-1 top-0 -translate-y-1/2'
          : [
              centred ? 'left-1/2 -translate-x-1/2' : 'right-0',
              placement.below ? 'top-full pt-1' : 'bottom-full pb-1',
            ],
        selected
          ? 'opacity-100'
          : 'pointer-events-none opacity-0 group-focus-within:pointer-events-auto group-focus-within:opacity-100 group-hover:pointer-events-auto group-hover:opacity-100'
      )}
    >
      {active && (
        <div className="flex items-center gap-0.5 rounded-md border bg-card p-0.5 shadow-sm">
          {canRun && (
            <IconButton
              {...toolProps}
              size="sm"
              className="nodrag nopan"
              label={ts('Run step')}
              icon={Play}
              onClick={() => onCommand({ kind: 'runStep', step })}
            />
          )}
          <IconButton
            {...toolProps}
            size="sm"
            className="nodrag nopan"
            label={ts('Open step')}
            icon={PanelRightOpen}
            shortcut={canvasShortcut('open')}
            onClick={() => onCommand({ kind: 'openInspector', step })}
          />
          {editable && (
            <IconButton
              {...toolProps}
              size="sm"
              className="nodrag nopan"
              label={ts('Duplicate step')}
              icon={CopyPlus}
              shortcut={canvasShortcut('duplicate')}
              onClick={() => onCommand({ kind: 'duplicate', step })}
            />
          )}
          <CopyStepButton {...toolProps} step={step} />
          {editable && (
            <IconButton
              {...toolProps}
              size="sm"
              className="nodrag nopan"
              tone="destructive"
              label={ts('Delete')}
              icon={Trash2}
              shortcut={canvasShortcut('delete')}
              onClick={() => onDeleteStep(step)}
            />
          )}
        </div>
      )}
    </div>
  );
}
