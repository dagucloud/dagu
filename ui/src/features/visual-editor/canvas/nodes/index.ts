// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ComponentType } from 'react';
import type { NodeProps } from '../engine';
import type { StepFlowNode } from '../types';
import { AgentNode } from './AgentNode';
import { HandlerNode } from './HandlerNode';
import { RouterNode } from './RouterNode';
import { StepNode } from './StepNode';
import { ToolNode } from './ToolNode';

/** Node components of the canvas, keyed by `CanvasNodeType`. */
export const nodeTypes = {
  step: StepNode,
  router: RouterNode,
  handler: HandlerNode,
  agent: AgentNode,
  tool: ToolNode,
} satisfies Record<string, ComponentType<NodeProps<StepFlowNode>>>;
