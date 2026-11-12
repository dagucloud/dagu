// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CalendarClock, MousePointerClick, Webhook } from 'lucide-react';
import { createContext, memo, useContext, type ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { DASHED } from '../canvas/edges/dash';
import { Handle, type NodeProps } from '../canvas/engine';
import { useCanvasActions } from '../canvas/context';
import { CountBadge } from '../canvas/CountBadge';
import { handlePosition } from '../canvas/nodes/NodeParts';
import { SOURCE_HANDLE, type StepFlowNode } from '../canvas/types';
import { launcherIdOf, type LauncherId } from './model';

export const LAUNCHER_ICONS = {
  manual: MousePointerClick,
  cron: CalendarClock,
  webhook: Webhook,
} as const;

/** Static message keys of the launcher names. */
export const LAUNCHER_LABELS: Record<LauncherId, string> = {
  manual: 'Manual',
  cron: 'Cron',
  webhook: 'Webhook',
};

/** What the canvas does when the user opens a launcher. */
export interface LauncherActions {
  open(id: LauncherId): void;
  /** The launcher whose panel is open. */
  active: LauncherId | null;
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

/**
 * A launcher: a way to start the workflow, drawn as a root of the graph. It
 * is not a step. A dashed card means that the launcher is not set up yet. A
 * card shows only its name; the state is in its tooltip and its accessible
 * name. The Cron card has a badge with the number of cron expressions, which
 * a card of no expression does not show.
 */
function LauncherNodeComponent({
  data,
}: NodeProps<StepFlowNode>): ReactElement {
  const { ts } = useI18n();
  const { direction, compact = false } = useCanvasActions();
  const { open, active } = useContext(LauncherActionsContext);
  const id = launcherIdOf(data.node.key);
  const state = data.launcher;
  if (!id || !state) return <div />;
  const Icon = LAUNCHER_ICONS[id];
  const dashed = state.status === 'dashed';
  const lit = active === id;
  const count = id === 'cron' ? (state.count ?? 0) : 0;
  const label =
    count > 0
      ? ts(count === 1 ? 'Cron, {count} schedule' : 'Cron, {count} schedules', {
          count,
        })
      : ts(LAUNCHER_LABELS[id]);
  const detail = ts(
    id === 'manual'
      ? 'Always on'
      : dashed
        ? 'Not set up'
        : id === 'cron'
          ? 'Scheduled'
          : 'Enabled'
  );
  return (
    <div
      data-testid={`ve-launcher-${id}`}
      data-launcher={id}
      data-status={state.status}
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
        <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
        {!compact && (
          <span className="min-w-0 truncate text-sm font-medium">
            {ts(LAUNCHER_LABELS[id])}
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
