// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  CalendarClock,
  MousePointerClick,
  Repeat,
  Webhook,
} from 'lucide-react';
import { createContext, memo, useContext, type ReactElement } from 'react';
import { NodeStatus } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import type { StaticTranslationValues } from '@/i18n/translateStatic';
import { cn } from '@/lib/utils';
import { DASHED } from '../canvas/edges/dash';
import { Handle, type NodeProps } from '../canvas/engine';
import { useCanvasActions } from '../canvas/context';
import { CountBadge } from '../canvas/CountBadge';
import { handlePosition } from '../canvas/nodes/NodeParts';
import { statusCardClass } from '../canvas/statusStyle';
import { SOURCE_HANDLE, type StepFlowNode } from '../canvas/types';
import { useSecondsLeft, type AliveState } from './alive';
import {
  DEFAULT_RETENTION_RUNS,
  launcherIdOf,
  type LauncherId,
  type LauncherState,
} from './model';

export const LAUNCHER_ICONS = {
  manual: MousePointerClick,
  schedule: CalendarClock,
  cron: CalendarClock,
  always: Repeat,
  webhook: Webhook,
} as const;

/** Static message keys of the launcher names. */
export const LAUNCHER_LABELS: Record<LauncherId, string> = {
  manual: 'Manual',
  schedule: 'Cron or Always on',
  cron: 'Cron',
  always: 'Always on',
  webhook: 'Webhook',
};

/** What the canvas does when the user opens a launcher. */
export interface LauncherActions {
  open(id: LauncherId): void;
  /** The launcher whose panel is open. */
  active: LauncherId | null;
  /** The state of the workflow of an Always on card; null while it is not alive. */
  alive?: AliveState | null;
}

export const LauncherActionsContext = createContext<LauncherActions>({
  open: () => undefined,
  active: null,
});

/**
 * Dashed outline of a launcher that is not set up. It is an SVG rectangle
 * with the dash pattern of the edges, so that the border and the edges have
 * the same dashes in every state. The rectangle is the outline: a state
 * changes its colour, never its dashes. Its parent must have the class
 * `group`, so that a hover or a focus of the button lights it.
 */
function DashedOutline({
  radius,
  lit,
}: {
  radius: number;
  lit: boolean;
}): ReactElement {
  return (
    <svg
      aria-hidden="true"
      data-testid="ve-launcher-outline"
      className="pointer-events-none absolute inset-0 h-full w-full overflow-visible"
    >
      <rect
        x={0.75}
        y={0.75}
        rx={radius}
        ry={radius}
        fill="none"
        strokeWidth={lit ? 2 : 1.5}
        strokeDasharray={DASHED}
        style={{
          width: 'calc(100% - 1.5px)',
          height: 'calc(100% - 1.5px)',
        }}
        className={cn(
          'group-hover:stroke-primary group-focus-visible:stroke-primary',
          lit ? 'stroke-primary' : 'stroke-muted-foreground'
        )}
      />
    </svg>
  );
}

type Translate = (source: string, values?: StaticTranslationValues) => string;

/** What the history of an Always on card keeps, in words. */
function keepsText(state: LauncherState, ts: Translate): string {
  const { retentionRuns: runs, retentionDays: days } = state;
  if (runs != null) {
    return ts(runs === 1 ? 'keeps {count} run' : 'keeps {count} runs', {
      count: runs,
    });
  }
  if (days != null) {
    return ts(
      days === 1 ? 'keeps runs for {count} day' : 'keeps runs for {count} days',
      { count: days }
    );
  }
  return ts('keeps {count} runs, or as many as the base configuration sets', {
    count: DEFAULT_RETENTION_RUNS,
  });
}

/**
 * The second line of a solid Always on card, which fits one line of a 176 px
 * card: the wait, and the policy when it is `unless-stopped`. A missing wait
 * is left out. The runs kept are in the tooltip only. Other cards have none.
 */
function launcherSummary(state: LauncherState, ts: Translate): string | null {
  if (!state.policy || state.status !== 'active') return null;
  const seconds = state.waitSec;
  if (state.policy === 'always') {
    return seconds ? ts('Wait {seconds} s', { seconds }) : null;
  }
  return seconds
    ? ts('Unless stopped, wait {seconds} s', { seconds })
    : ts('Unless stopped');
}

/**
 * The second line while the wait counts down, then while the next run
 * starts. A wait with no second left is over.
 */
function aliveLine(
  alive: AliveState | null,
  seconds: number,
  ts: Translate
): string | null {
  if (alive?.phase === 'waiting' && seconds > 0) {
    return ts('Next run in {seconds} s', { seconds });
  }
  return alive && alive.phase !== 'running' ? ts('Starting') : null;
}

/** The alive state in words, for the tooltip and the accessible name. */
function aliveDetail(
  alive: AliveState,
  seconds: number,
  ts: Translate
): string {
  if (alive.phase === 'running') return ts('running');
  if (alive.phase === 'waiting' && seconds > 0) {
    return ts('next run in {seconds} s', { seconds });
  }
  return ts('starting');
}

/** The state of a launcher in words, for its tooltip and its accessible name. */
function launcherDetail(
  id: LauncherId,
  state: LauncherState,
  ts: Translate
): string {
  if (id === 'manual') return ts('Available');
  if (id === 'schedule') {
    return ts('Not set up. Choose a cron schedule or Always on.');
  }
  if (state.status === 'dashed') return ts('Not set up');
  if (id === 'cron') return ts('Scheduled');
  if (id === 'always') {
    const policy = ts(
      state.policy === 'unless-stopped'
        ? 'Restarts unless stopped'
        : 'Restarts after each run'
    );
    const waited = state.waitSec
      ? ts('{state}, wait {seconds} s', {
          state: policy,
          seconds: state.waitSec,
        })
      : policy;
    return ts('{state}, {history}', {
      state: waited,
      history: keepsText(state, ts),
    });
  }
  return ts('Enabled');
}

/**
 * A launcher: a way to start the workflow, drawn as a root of the graph. It
 * is not a step. A dashed card means that the launcher is not set up yet. A
 * card shows its name; the state is in its tooltip and its accessible name.
 * A solid Always on card has a second line with its wait, except in the
 * simplified view. While its workflow is alive, the card takes the colours
 * of a running step during a run, and its second line counts down the wait
 * before the next run. The Cron card has a badge with the number of cron
 * expressions, which a card of no expression does not show.
 */
function LauncherNodeComponent({
  data,
}: NodeProps<StepFlowNode>): ReactElement {
  const { ts } = useI18n();
  const { direction, compact = false } = useCanvasActions();
  const {
    open,
    active,
    alive: workflowAlive = null,
  } = useContext(LauncherActionsContext);
  const id = launcherIdOf(data.node.key);
  // Only the Always on card counts down the wait, once a second.
  const seconds = useSecondsLeft(
    id === 'always' && workflowAlive?.phase === 'waiting'
      ? workflowAlive.nextRunAt
      : null
  );
  const state = data.launcher;
  if (!id || !state) return <div />;
  const Icon = LAUNCHER_ICONS[id];
  const dashed = state.status === 'dashed';
  const lit = active === id;
  const alive = id === 'always' && !dashed ? workflowAlive : null;
  const count = id === 'cron' ? (state.count ?? 0) : 0;
  const label =
    count > 0
      ? ts(count === 1 ? 'Cron, {count} schedule' : 'Cron, {count} schedules', {
          count,
        })
      : ts(LAUNCHER_LABELS[id]);
  // The state is in words too: the colour of the card is not the only sign.
  const detail = alive
    ? ts('{detail}, {live}', {
        detail: launcherDetail(id, state, ts),
        live: aliveDetail(alive, seconds, ts),
      })
    : launcherDetail(id, state, ts);
  const summary = compact
    ? null
    : (aliveLine(alive, seconds, ts) ?? launcherSummary(state, ts));
  return (
    <div
      data-testid={`ve-launcher-${id}`}
      data-launcher={id}
      data-status={state.status}
      data-alive={alive?.phase}
      className="relative"
    >
      <button
        type="button"
        aria-label={`${label}: ${detail}`}
        title={`${label}: ${detail}`}
        aria-pressed={lit}
        onClick={(event) => {
          event.stopPropagation();
          open(id);
        }}
        className={cn(
          'group nodrag nopan relative flex items-center gap-2 bg-card text-left shadow-sm transition-colors focus-visible:outline-none',
          compact
            ? 'h-14 w-14 justify-center rounded-md'
            : 'h-14 w-44 rounded-full px-3',
          dashed
            ? 'bg-card/60 text-muted-foreground'
            : 'border-[1.5px] border-muted-foreground text-card-foreground focus-visible:ring-2 focus-visible:ring-ring',
          alive?.phase === 'running' && statusCardClass(NodeStatus.Running),
          // A solid ring would hide the dashes of the outline.
          lit && !dashed && 'ring-2 ring-primary'
        )}
      >
        {dashed && <DashedOutline radius={compact ? 6 : 28} lit={lit} />}
        <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
        {!compact && (
          <span className="flex min-w-0 flex-col">
            <span className="truncate text-sm font-medium">
              {ts(LAUNCHER_LABELS[id])}
            </span>
            {summary && (
              <span
                data-testid="ve-launcher-summary"
                className="truncate text-xs leading-4 text-muted-foreground"
              >
                {summary}
              </span>
            )}
          </span>
        )}
        {count > 0 && (
          <CountBadge
            data-testid="ve-launcher-count"
            aria-hidden="true"
            count={count}
            corner
          />
        )}
      </button>
      <Handle
        id={SOURCE_HANDLE}
        type="source"
        position={handlePosition(direction, 'source')}
        isConnectable={false}
        className="!h-2 !w-2 !rounded-full !border !border-border !bg-muted-foreground"
      />
    </div>
  );
}

export const LauncherNode = memo(LauncherNodeComponent);
