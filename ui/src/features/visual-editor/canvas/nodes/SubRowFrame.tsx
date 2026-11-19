// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { type CSSProperties, type ReactElement, type ReactNode } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import type { CanvasDirection } from '../../contracts';
import { Handle, Position } from '../engine';
import { cardSize, compactCardOffset } from '../layout';
import {
  type SlotConnector,
  type SubRowPlan,
  type SubSide,
  type SubSlot,
} from '../subRow';
import { slotHandleId } from '../types';
import { SUB_DIAMOND_CLASS } from './SubLeaf';

/** Size of the card of a chat step, in a normal or a compact canvas. */
function chatCardSize(
  plan: Pick<SubRowPlan, 'base'>,
  compact: boolean
): { width: number; height: number } {
  // A normal card is its base; a compact card is a tile in a box with its caption.
  return compact
    ? cardSize({ key: '', chat: true }, true)
    : { width: plan.base.width, height: plan.base.height };
}

/**
 * Where the entry and the exit of a chat step are in its box. Top to bottom:
 * the middle of the top and of the bottom side of the step, not of the box,
 * which is taller than the step when the sub-nodes beside it are: the main
 * flow then runs between the two columns and never crosses them. Left to
 * right: the middle of the left and of the right side of the card, not of the
 * box, which is wider than the card when the row is: the main flow then runs
 * beside the row and never crosses it.
 */
export function chatAnchors(
  plan: Pick<SubRowPlan, 'width' | 'height' | 'base' | 'card'>,
  direction: CanvasDirection,
  compact = false
): { target: CSSProperties; source: CSSProperties } {
  if (direction !== 'LR') {
    return {
      target: { left: plan.width / 2, top: plan.card.y },
      source: { left: plan.width / 2, top: plan.base.y + plan.base.height },
    };
  }
  const card = chatCardSize(plan, compact);
  const left =
    plan.base.x + (compact ? compactCardOffset(direction, card).left : 0);
  return {
    target: { left, top: plan.height / 2 },
    source: { left: left + card.width, top: plan.height / 2 },
  };
}

/**
 * Where the "+" stalk of a chat step leaves, and the side it goes to. Top to
 * bottom it hangs from the middle of the lower side of the step, between the
 * two columns of sub-nodes. Left to right it leaves from the middle of the
 * right side of the card, so that it hangs from the step and not from its
 * sub-nodes above and under it.
 */
export function chatStalk(
  plan: Pick<SubRowPlan, 'width' | 'base' | 'card'>,
  compact: boolean,
  direction: CanvasDirection
): { side: Position.Right | Position.Bottom; at: CSSProperties } {
  if (direction !== 'LR') {
    return {
      side: Position.Bottom,
      at: { left: plan.width / 2, top: plan.base.y + plan.base.height },
    };
  }
  const card = chatCardSize(plan, compact);
  if (!compact) {
    return {
      side: Position.Right,
      at: {
        left: plan.base.x + card.width,
        top: plan.base.y + card.height / 2,
      },
    };
  }
  const offset = compactCardOffset(direction, card);
  return {
    side: Position.Right,
    at: {
      left: plan.base.x + offset.left + card.width,
      top: plan.base.y + offset.top + card.height / 2,
    },
  };
}

/** The side of the handle of a connector, in the sense of the flow. */
function connectorPosition(side: SubSide): Position {
  switch (side) {
    case 'left':
      return Position.Left;
    case 'right':
      return Position.Right;
    case 'top':
      return Position.Top;
    default:
      return Position.Bottom;
  }
}

/**
 * Where the label of a slot is: right under a connector on the lower edge,
 * right above a connector on the upper edge, and above the link when the
 * connector is on a side, on the side of the sub-nodes.
 */
function labelAt(at: SlotConnector): {
  style: CSSProperties;
  className: string;
} {
  switch (at.side) {
    case 'left':
      return {
        style: { left: at.x - 6, top: at.y - 8 },
        className: '-translate-x-full -translate-y-full',
      };
    case 'right':
      return {
        style: { left: at.x + 6, top: at.y - 8 },
        className: '-translate-y-full',
      };
    case 'top':
      return {
        style: { left: at.x, top: at.y - 9 },
        className: '-translate-x-1/2 -translate-y-full',
      };
    default:
      return {
        style: { left: at.x, top: at.y + 9 },
        className: '-translate-x-1/2',
      };
  }
}

/**
 * The box of a chat step with its sub-nodes. The step (`children`) sits in
 * the box, and the connectors of the Model, Prompt, Skills, Tools and DAGs on its
 * sides (or on its upper and lower edges, left to right), each with its label next to it. The connector
 * of a sub-node that the user moved sits on the side that faces it. `anchors` are
 * the handles of the entry and the exit of the step and its "+" stalk, which
 * belong to the box, not to the card.
 */
export function SubRowFrame({
  plan,
  anchors,
  children,
}: {
  plan: SubRowPlan;
  anchors: ReactNode;
  children: ReactNode;
}): ReactElement {
  const { ts } = useI18n();
  // The connectors of the slots are fixed: a moved sub-node never moves them.
  const slots = plan.slots;
  const labels: Record<SubSlot, string> = {
    prompt: ts('Prompt'),
    model: ts('Model'),
    tools: ts('Tools'),
    skills: ts('Skills'),
    dags: ts('DAGs'),
  };
  return (
    <div
      data-testid="ve-canvas-sub-frame"
      // The box covers the sub-nodes: only the card and the handles take the pointer.
      className="pointer-events-none relative"
      style={{ width: plan.width, height: plan.height }}
    >
      {anchors}
      <div
        className="pointer-events-auto absolute"
        style={{
          left: plan.base.x,
          top: plan.base.y,
          width: plan.base.width,
          height: plan.base.height,
        }}
      >
        {children}
      </div>
      {plan.present.map((slot) => {
        const at = slots[slot];
        const label = labelAt(at);
        return (
          <div key={slot}>
            <Handle
              id={slotHandleId(slot)}
              type="source"
              position={connectorPosition(at.side)}
              isConnectable={false}
              style={{ left: at.x, top: at.y }}
              className={SUB_DIAMOND_CLASS}
            />
            <span
              data-testid={`ve-canvas-slot-${slot}`}
              data-side={at.side}
              style={label.style}
              // The label sits next to its connector. Its solid background and
              // padding keep the link from touching the text.
              className={`pointer-events-none absolute rounded-sm bg-background px-1.5 text-[10px] leading-3 text-muted-foreground ${label.className}`}
            >
              {labels[slot]}
            </span>
          </div>
        );
      })}
    </div>
  );
}
