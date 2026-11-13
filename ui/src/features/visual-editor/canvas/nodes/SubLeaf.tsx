// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { TriangleAlert, type LucideIcon } from 'lucide-react';
import { useContext, type ReactElement, type ReactNode } from 'react';
import { cn } from '@/lib/utils';
import type { CanvasNode } from '../../contracts';
import { useCanvasActions } from '../context';
import { Handle, Position } from '../engine';
import { NodeContext } from '../engine/Handle';
import {
  SUB_ICON_SIZE,
  SUB_TILE_SIZE,
  slotSide,
  type SubSlot,
} from '../subRow';
import { isModelSubNode, satelliteOwner } from '../toolSatellites';
import { useSubLinks } from '../subLinks';
import { TARGET_HANDLE } from '../types';
import { CARD_OUTLINE } from './NodeParts';

/**
 * A small diamond, a square turned by 45 degrees, in the colour of the links
 * of the sub-nodes. It is the form of the slot connectors and of the entry of
 * a sub-node.
 */
export const SUB_DIAMOND_CLASS =
  '!h-2.5 !w-2.5 !rotate-45 !rounded-[2px] !border-0 !bg-muted-foreground';

/**
 * The stub of the side entry of a round node: a short dashed line at the
 * height of the icon, from the edge of the icon to the entry on the side of
 * the node. The node is wider than its icon, so the link of the step ends at
 * the side of the node and the stub carries it on to the icon.
 */
export function leafStub(side: 'left' | 'right'): {
  top: number;
  left: string;
  right: string;
} {
  const edge = `calc(50% + ${SUB_ICON_SIZE / 2}px)`;
  return side === 'left'
    ? { top: SUB_ICON_SIZE / 2, left: edge, right: '0px' }
    : { top: SUB_ICON_SIZE / 2, left: '0px', right: edge };
}

/**
 * The entry of a sub-node: a small diamond on the side that faces its step,
 * over the icon. Top to bottom the sub-nodes sit beside the step, so the entry
 * is on their right (the Model and the Prompt, left of the step) or on their
 * left (the Skills and the Tools, right of it). Left to right the Model and
 * the Prompt sit above the step, so the entry is on their bottom; the Skills
 * and the Tools sit under it, so it is on their top. `leaf` puts a side entry
 * at the height of the round icon of the node, which has its name under it,
 * with a short dashed stub from the entry to the icon.
 * `hidden` keeps the handle for the link but draws nothing. A sub-node that
 * the user moved to another side of the step has its entry on the side that
 * faces the step.
 */
export function SubHandle({
  slot,
  leaf = false,
  hidden = false,
}: {
  slot: SubSlot;
  leaf?: boolean;
  hidden?: boolean;
}): ReactElement {
  const { direction } = useCanvasActions();
  const links = useSubLinks();
  const id = useContext(NodeContext)?.id;
  // A sub-node that the user moved faces the side of the step where it sits now.
  const side =
    (id !== undefined && links.sides[id]) || slotSide(slot, direction);
  const position = {
    left: Position.Right,
    right: Position.Left,
    top: Position.Bottom,
    bottom: Position.Top,
  }[side];
  const sideways = leaf && (side === 'left' || side === 'right');
  return (
    <>
      {sideways && !hidden && (
        <span
          aria-hidden="true"
          data-testid="ve-canvas-sub-stub"
          style={leafStub(side)}
          className="pointer-events-none absolute border-t-[1.25px] border-dashed border-muted-foreground"
        />
      )}
      <Handle
        id={TARGET_HANDLE}
        type="target"
        position={position}
        isConnectable={false}
        {...(sideways ? { style: { top: SUB_ICON_SIZE / 2 } } : {})}
        className={cn(SUB_DIAMOND_CLASS, hidden && '!opacity-0')}
      />
    </>
  );
}

/** The side of a round sub-node: a ring when selected, a ring for the keyboard focus. */
export const SUB_SELECTED_CLASS = 'ring-2 ring-primary';
export const SUB_FOCUS_CLASS =
  'group-focus-visible:ring-2 group-focus-visible:ring-ring';

/**
 * Opens the form of the step of a sub-node, with the Model section open and
 * in view: the section that the sub-node shows.
 */
export function useOpenSubNode(node: CanvasNode): () => void {
  const { onCommand } = useCanvasActions();
  return () => {
    const step = satelliteOwner(node.key);
    if (step !== null && isModelSubNode(node)) {
      onCommand({ kind: 'openForm', step, section: 'model' });
    }
  };
}

/**
 * The round sub-node of the model of a chat step: a round icon, its name
 * under it on up to two lines, and a muted line of detail, in the box of the
 * layout. The name wraps; only the detail is cut with an ellipsis. `overlays`
 * sit on the circle. A click selects it with its step; a double click, Enter
 * or Space opens the YAML of the step on its part. The selected node has a
 * ring.
 */
export function SubLeaf({
  node,
  selected = false,
  icon: Icon,
  label,
  labelExtra,
  detail,
  title,
  dashed = false,
  dimmed = false,
  attrs,
  overlays,
  iconClassName,
}: {
  node: CanvasNode;
  selected?: boolean | undefined;
  icon: LucideIcon;
  label: string;
  /** After the name, never cut: for example the count of more models. */
  labelExtra?: ReactNode;
  detail?: string | undefined;
  title?: string | undefined;
  dashed?: boolean;
  dimmed?: boolean;
  /** `data-*` attributes of the root: the test id, the shape, the run marks. */
  attrs: Record<string, string | boolean | undefined>;
  overlays?: ReactNode;
  iconClassName?: string;
}): ReactElement {
  const open = useOpenSubNode(node);
  return (
    <div
      {...attrs}
      title={title}
      role="button"
      tabIndex={0}
      aria-label={label}
      aria-current={selected || undefined}
      data-selected={selected || undefined}
      onKeyDown={(event) => {
        if (event.key !== 'Enter' && event.key !== ' ') return;
        event.preventDefault();
        event.stopPropagation();
        open();
      }}
      onDoubleClick={(event) => {
        event.stopPropagation();
        open();
      }}
      className={cn(
        'group relative flex h-full w-full cursor-pointer flex-col items-center text-card-foreground outline-none',
        dimmed && 'opacity-50'
      )}
    >
      <SubHandle slot="model" leaf />
      <span
        data-testid="ve-canvas-sub-icon"
        style={{ width: SUB_ICON_SIZE, height: SUB_ICON_SIZE }}
        className={cn(
          'relative flex shrink-0 items-center justify-center rounded-full bg-card',
          CARD_OUTLINE,
          dashed && 'border-dashed',
          selected ? SUB_SELECTED_CLASS : SUB_FOCUS_CLASS
        )}
      >
        <Icon
          className={cn('h-7 w-7 text-muted-foreground', iconClassName)}
          aria-hidden="true"
        />
        {overlays}
      </span>
      <span className="mt-1.5 line-clamp-2 w-full break-words text-center text-xs font-medium leading-4">
        {label}
        {labelExtra && <span className="ml-1">{labelExtra}</span>}
      </span>
      {detail && (
        <span className="w-full truncate text-center text-[11px] leading-[14px] text-muted-foreground">
          {detail}
        </span>
      )}
    </div>
  );
}

/**
 * The tile of a slot card or of the Prompt card in a compact canvas: the
 * icon of the slot over the count of its entries, in a square of
 * `SUB_TILE_SIZE`. The tooltip lists the entries. `problem` puts a red mark
 * on the tile, with its text. A double click, Enter or Space opens the list
 * of the slot; with `openOnClick`, a click opens it too.
 */
export function SubTile({
  slot,
  selected = false,
  icon: Icon,
  label,
  count,
  title,
  problem,
  attrs,
  openOnClick = false,
  onOpen,
}: {
  slot: SubSlot;
  selected?: boolean | undefined;
  icon: LucideIcon;
  label: string;
  count?: number;
  title: string;
  problem?: string | undefined;
  attrs: Record<string, string | boolean | undefined>;
  openOnClick?: boolean;
  onOpen(): void;
}): ReactElement {
  return (
    <div
      {...attrs}
      title={title}
      role="button"
      tabIndex={0}
      aria-label={count === undefined ? label : `${label} ${count}`}
      aria-current={selected || undefined}
      data-selected={selected || undefined}
      style={{ width: SUB_TILE_SIZE, height: SUB_TILE_SIZE }}
      onClick={
        openOnClick
          ? (event) => {
              event.stopPropagation();
              onOpen();
            }
          : undefined
      }
      onDoubleClick={(event) => {
        event.stopPropagation();
        onOpen();
      }}
      onKeyDown={(event) => {
        if (event.key !== 'Enter' && event.key !== ' ') return;
        event.preventDefault();
        event.stopPropagation();
        onOpen();
      }}
      className={cn(
        'group relative flex cursor-pointer flex-col items-center justify-center gap-0.5 rounded-md bg-card text-card-foreground outline-none hover:bg-muted/30 focus-visible:ring-2 focus-visible:ring-ring',
        CARD_OUTLINE,
        selected && SUB_SELECTED_CLASS
      )}
    >
      <SubHandle slot={slot} />
      <Icon className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
      {count !== undefined && (
        <span
          data-testid="ve-canvas-slot-count"
          className="text-[11px] leading-3 text-muted-foreground"
        >
          {count}
        </span>
      )}
      {problem && (
        <span
          role="img"
          aria-label={problem}
          title={problem}
          className="absolute -right-1 -top-1 rounded-full bg-card p-0.5 text-destructive"
        >
          <TriangleAlert className="h-3 w-3" aria-hidden="true" />
        </span>
      )}
    </div>
  );
}
