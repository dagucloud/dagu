// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  CalendarClock,
  MousePointerClick,
  Repeat,
  Webhook,
} from 'lucide-react';
import { createContext, memo, useContext, type ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import type { StaticTranslationValues } from '@/i18n/translateStatic';
import { cn } from '@/lib/utils';
import { DASHED } from '../canvas/edges/dash';
import { Handle, type NodeProps } from '../canvas/engine';
import { useCanvasActions } from '../canvas/context';
import { CountBadge } from '../canvas/CountBadge';
import { handlePosition } from '../canvas/nodes/NodeParts';
import { SOURCE_HANDLE, type StepFlowNode } from '../canvas/types';
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
  /** True while the workflow of an Always on card is alive: its icon turns. */
  alive?: boolean;
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

/** The runs that an Always on card keeps, and whether the file sets them. */
function keptRuns(state: LauncherState): { count: number; set: boolean } {
  const set = state.retentionRuns != null;
  return { count: state.retentionRuns ?? DEFAULT_RETENTION_RUNS, set };
}

function keepsText(count: number, ts: Translate): string {
  return ts(count === 1 ? 'keeps {count} run' : 'keeps {count} runs', {
    count,
  });
}

/**
 * The second line of a solid Always on card. It is short, to fit one line of
 * a 176 px card. The first line says Always, so `always` shows the wait and
 * the runs kept, and `unless-stopped` shows its name and the wait: the runs
 * kept are in the tooltip. A missing wait is left out. Other cards have none.
 */
function launcherSummary(state: LauncherState, ts: Translate): string | null {
  if (!state.policy || state.status !== 'active') return null;
  const always = state.policy === 'always';
  const { count } = keptRuns(state);
  return [
    ...(always ? [] : [ts('unless stopped')]),
    ...(state.waitSec ? [ts('{seconds} s', { seconds: state.waitSec })] : []),
    ...(always
      ? [ts(count === 1 ? '{count} run' : '{count} runs', { count })]
      : []),
  ].join(' · ');
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
    const { count, set } = keptRuns(state);
    return ts('{state}, {history}', {
      state: waited,
      history: set
        ? keepsText(count, ts)
        : ts('keeps {count} runs (default)', { count }),
    });
  }
  return ts('Enabled');
}

/**
 * A launcher: a way to start the workflow, drawn as a root of the graph. It
 * is not a step. A dashed card means that the launcher is not set up yet. A
 * card shows its name; the state is in its tooltip and its accessible name.
 * A solid Always on card has a second line with the policy and its numbers,
 * except in the simplified view, and its icon turns while the workflow is
 * alive. The Cron card has a badge with the number
 * of cron expressions, which a card of no expression does not show.
 */
function LauncherNodeComponent({
  data,
}: NodeProps<StepFlowNode>): ReactElement {
  const { ts } = useI18n();
  const { direction, compact = false } = useCanvasActions();
  const { open, active, alive = false } = useContext(LauncherActionsContext);
  const id = launcherIdOf(data.node.key);
  const state = data.launcher;
  if (!id || !state) return <div />;
  const Icon = LAUNCHER_ICONS[id];
  const dashed = state.status === 'dashed';
  const lit = active === id;
  const turning = alive && id === 'always' && !dashed;
  const count = id === 'cron' ? (state.count ?? 0) : 0;
  const label =
    count > 0
      ? ts(count === 1 ? 'Cron, {count} schedule' : 'Cron, {count} schedules', {
          count,
        })
      : ts(LAUNCHER_LABELS[id]);
  const detail = launcherDetail(id, state, ts);
  const summary = compact ? null : launcherSummary(state, ts);
  return (
    <div
      data-testid={`ve-launcher-${id}`}
      data-launcher={id}
      data-status={state.status}
      data-alive={turning ? 'true' : undefined}
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
          // A solid ring would hide the dashes of the outline.
          lit && !dashed && 'ring-2 ring-primary'
        )}
      >
        {dashed && <DashedOutline radius={compact ? 6 : 28} lit={lit} />}
        <Icon
          className={cn(
            'h-4 w-4 shrink-0',
            // One turn in 3 s. Without motion the icon takes the accent colour.
            turning &&
              'animate-[spin_3s_linear_infinite] motion-reduce:animate-none motion-reduce:text-primary'
          )}
          aria-hidden="true"
        />
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
