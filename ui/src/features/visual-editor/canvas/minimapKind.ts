// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { FlowNode, MinimapKind } from './engine';
import type { CanvasDirection } from '../contracts';
import type { Rect } from './engine/geometry';
import { chatPlan } from './layout';
import type { CanvasNodeWithRoutes } from './types';

/**
 * What a node of the canvas is in the overview. An agent step (an `agent.run`
 * step, or a chat step with a model) is drawn dark, so that it is found at a
 * glance, and the cards that hang from it (Model, Prompt, Skills, Tools, DAGs)
 * in a middle grey. The small "+" of an empty slot is not drawn. Any other
 * node is plain.
 */
export function minimapKindOf(node: FlowNode): MinimapKind | undefined {
  switch (node.type) {
    case 'agent':
      return 'agent';
    case 'modelsat':
    case 'promptsat':
    case 'slotcard':
    case 'dagcard':
      return 'satellite';
    case 'addsat':
      return 'hidden';
    case 'step': {
      const step = (node.data as { node?: CanvasNodeWithRoutes }).node;
      return step?.agent || step?.chat ? 'agent' : undefined;
    }
    default:
      return undefined;
  }
}

/**
 * The part of the box of a step that the overview draws. The box of a step with
 * sub-nodes holds its Model, Prompt, Skills, Tools and DAGs: the overview draws
 * the card of the step, so that the sub-nodes show as parts of their own.
 */
export function minimapRectOf(
  node: FlowNode,
  box: Rect,
  compact: boolean,
  direction: CanvasDirection
): Rect | undefined {
  if (node.type !== 'step') return undefined;
  const step = (node.data as { node?: CanvasNodeWithRoutes }).node;
  const plan = step?.subRow ? chatPlan(step, compact, direction) : null;
  if (!plan) return undefined;
  return {
    x: box.x + plan.card.x,
    y: box.y + plan.card.y,
    width: plan.card.width,
    height: plan.card.height,
  };
}
