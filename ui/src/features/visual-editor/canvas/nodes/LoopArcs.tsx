// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { CanvasNode } from '../../contracts';
import {
  LOOP_BOTTOM_HEAD,
  LOOP_BOTTOM_INSET,
  LOOP_BOTTOM_PATH,
  LOOP_TOP_INSET,
  LOOP_FOOT,
  LOOP_HEAD,
  LOOP_HEIGHT,
  LOOP_PATH,
  LOOP_SCALE,
  LOOP_WIDTH,
  loopArcs,
  type LoopArc,
  type LoopPlacement,
} from '../loopArcs';
import type { CanvasNodeWithRoutes } from '../types';

const LABEL =
  'absolute whitespace-nowrap rounded border border-border bg-card px-1 text-[10px] leading-[14px] tabular-nums text-muted-foreground';

function Arc({
  arc,
  placement,
  compact,
}: {
  arc: LoopArc;
  placement: LoopPlacement;
  compact: boolean;
}): ReactElement {
  // On the upper edge the retry is at the right corner and the repeat at the
  // left one. On the left side the arcs share the side: the retry low, the
  // repeat high, clear of the handle at the middle.
  const right = placement === 'side' ? false : arc.side === 'right';
  const common = {
    role: 'img',
    'aria-label': arc.title,
    title: arc.title,
    'data-testid': 've-loop-arc',
    'data-kind': arc.kind,
    'data-side': arc.side,
    'data-placement': placement,
  } as const;
  const stroke = {
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.5,
    strokeLinecap: 'round',
    // The repeat is dashed: it loops after a run, the retry after a failure.
    strokeDasharray: arc.kind === 'repeat' ? '3 2.5' : undefined,
  } as const;
  if (placement === 'top') {
    const up = placement === 'top';
    const inset = up && compact ? LOOP_TOP_INSET : LOOP_BOTTOM_INSET;
    return (
      <span
        {...common}
        // A box with no height on the lower (or upper) edge of the card: the
        // arc and its label hang outside of it, in the colour of the links.
        // Over the caption of a compact card.
        style={{
          width: LOOP_WIDTH,
          ...(right ? { right: inset } : { left: inset }),
        }}
        className={cn(
          'pointer-events-none absolute z-10 h-0 select-none',
          up ? 'top-0' : 'bottom-0'
        )}
      >
        <svg
          aria-hidden="true"
          width={LOOP_WIDTH}
          height={LOOP_HEIGHT}
          viewBox="0 0 16 14"
          {...stroke}
          className={cn(
            'absolute left-0 overflow-visible text-muted-foreground',
            up ? 'bottom-0 -scale-y-100' : 'top-0'
          )}
        >
          <path d={LOOP_BOTTOM_PATH} />
          <path d={LOOP_BOTTOM_HEAD} fill="currentColor" stroke="none" />
        </svg>
        {arc.label && (
          <span
            style={up ? { bottom: 10 * LOOP_SCALE } : { top: 10 * LOOP_SCALE }}
            className={cn(LABEL, 'left-1/2 -translate-x-1/2')}
          >
            {arc.label}
          </span>
        )}
      </span>
    );
  }
  return (
    <span
      {...common}
      style={
        arc.kind === 'repeat'
          ? { top: LOOP_FOOT, height: LOOP_HEIGHT }
          : { bottom: LOOP_FOOT, height: LOOP_HEIGHT }
      }
      // A box with no width on the side of the card: the arc and its label
      // hang outside of it, in the colour of the links.
      className={cn(
        'pointer-events-none absolute z-10 w-0 select-none',
        right ? 'right-0' : 'left-0'
      )}
    >
      <svg
        aria-hidden="true"
        width={LOOP_WIDTH}
        height={LOOP_HEIGHT}
        viewBox="0 0 16 14"
        {...stroke}
        className={cn(
          'absolute top-0 overflow-visible text-muted-foreground',
          right ? 'left-0' : 'right-0 -scale-x-100'
        )}
      >
        <path d={LOOP_PATH} />
        <path d={LOOP_HEAD} fill="currentColor" stroke="none" />
      </svg>
      {arc.label && (
        <span
          style={right ? { left: 12 * LOOP_SCALE } : { right: 12 * LOOP_SCALE }}
          className={cn(LABEL, 'top-1/2 -translate-y-1/2')}
        >
          {arc.label}
        </span>
      )}
    </span>
  );
}

/**
 * The loops of a step as small return arcs on its card: the retry on the
 * right, the repeat on the left, each with its number of tries (`×12`, `∞` for
 * a repeat with no limit). They come on top of the chips and the repeat badge,
 * and the tooltip of an arc is the policy. A plain card has them on its sides;
 * the card of a step with sub-nodes has them on its lower edge (`bottom`), as
 * its sides hold the handles of the sub-nodes, or on its upper edge (`top`)
 * when it is compact and has a caption under it. Nothing for a step with no
 * loop. The card that holds them is `relative` or `absolute`.
 */
export function LoopArcs({
  node,
  placement = 'side',
  compact = false,
}: {
  node: CanvasNode & Pick<CanvasNodeWithRoutes, 'retry'>;
  placement?: LoopPlacement;
  compact?: boolean;
}): ReactElement | null {
  const { ts } = useI18n();
  const arcs = loopArcs(ts, node);
  if (arcs.length === 0) return null;
  return (
    <>
      {arcs.map((arc) => (
        <Arc key={arc.kind} arc={arc} placement={placement} compact={compact} />
      ))}
    </>
  );
}
