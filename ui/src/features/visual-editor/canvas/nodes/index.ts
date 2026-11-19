// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ComponentType } from 'react';
import type { NodeProps } from '../engine';
import type { StepFlowNode } from '../types';
import { AddSubNode } from './AddSubNode';
import { AgentNode } from './AgentNode';
import { DagCardNode } from './DagCardNode';
import { HandlerNode } from './HandlerNode';
import { ModelSubNode } from './ModelSubNode';
import { PromptCardNode } from './PromptCardNode';
import { RouterNode } from './RouterNode';
import { SlotCardNode } from './SlotCardNode';
import { StepNode } from './StepNode';
import { ToolNode } from './ToolNode';

/** Node components of the canvas, keyed by `CanvasNodeType`. */
export const nodeTypes = {
  step: StepNode,
  router: RouterNode,
  handler: HandlerNode,
  agent: AgentNode,
  tool: ToolNode,
  slotcard: SlotCardNode,
  dagcard: DagCardNode,
  modelsat: ModelSubNode,
  promptsat: PromptCardNode,
  addsat: AddSubNode,
} satisfies Record<string, ComponentType<NodeProps<StepFlowNode>>>;
