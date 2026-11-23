// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { CanvasNode } from '../../contracts';
import {
  LOOP_ARC_HEAD,
  LOOP_ARC_PATH,
  LOOP_COMPACT_INSET,
  LOOP_HEIGHT,
  LOOP_INSET,
  LOOP_SCALE,
  LOOP_WIDTH,
  loopArcs,
  type LoopArc,
} from '../loopArcs';
import type { CanvasNodeWithRoutes } from '../types';

function Arc({
  arc,
  compact,
}: {
  arc: LoopArc;
  compact: boolean;
}): ReactElement {
  const right = arc.side === 'right';
  const inset = compact ? LOOP_COMPACT_INSET : LOOP_INSET;
  return (
    <span
      role="img"
      aria-label={arc.title}
      title={arc.title}
      data-testid="ve-loop-arc"
      data-kind={arc.kind}
      data-side={arc.side}
      // A box with no height on the upper edge of the card: the arc and its
      // label hang above it. The box takes the pointer, so that its tooltip
      // shows, and no click: the card under it has none there.
      style={{
        width: LOOP_WIDTH,
        ...(right ? { right: inset } : { left: inset }),
      }}
      className="absolute top-0 z-10 h-0 select-none"
    >
      <svg
        aria-hidden="true"
        width={LOOP_WIDTH}
        height={LOOP_HEIGHT}
        viewBox="0 0 16 14"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.5}
        strokeLinecap="round"
        // The repeat is dashed: it loops after a run, the retry after a failure.
        strokeDasharray={arc.kind === 'repeat' ? '3 2.5' : undefined}
        className="absolute bottom-0 left-0 -scale-y-100 overflow-visible text-muted-foreground"
      >
        <path d={LOOP_ARC_PATH} />
        <path d={LOOP_ARC_HEAD} fill="currentColor" stroke="none" />
      </svg>
      {arc.label && (
        <span
          style={{ bottom: 10 * LOOP_SCALE }}
          // The label grows away from the middle of the edge, where a link
          // can enter the card.
          className={cn(
            'absolute whitespace-nowrap rounded border border-border bg-card px-1 text-[10px] leading-[14px] tabular-nums text-muted-foreground',
            right ? 'left-1/2' : 'right-1/2'
          )}
        >
          {arc.label}
        </span>
      )}
    </span>
  );
}

/**
 * The loops of a step as small return arcs on the upper edge of its card:
 * the retry at the right corner, the repeat at the left one, each with its
 * most tries (`max 12`, `∞` for a repeat with no limit). The tooltip of an
 * arc is the whole policy. Nothing for a step with no loop. The card that
 * holds them is `relative` or `absolute`.
 */
export function LoopArcs({
  node,
  compact = false,
}: {
  node: CanvasNode & Pick<CanvasNodeWithRoutes, 'retry'>;
  compact?: boolean;
}): ReactElement | null {
  const { ts } = useI18n();
  const arcs = loopArcs(ts, node);
  if (arcs.length === 0) return null;
  return (
    <>
      {arcs.map((arc) => (
        <Arc key={arc.kind} arc={arc} compact={compact} />
      ))}
    </>
  );
}
