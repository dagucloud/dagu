// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ComponentType } from 'react';
import type { EdgeProps } from '../engine';
import type { CanvasFlowEdge } from '../types';
import { DependencyEdge } from './DependencyEdge';
import { LockedEdge } from './LockedEdge';

/** Edge components of the canvas, keyed by `CanvasEdgeType`. */
export const edgeTypes = {
  depends: DependencyEdge,
  route: DependencyEdge,
  inferred: LockedEdge,
  order: LockedEdge,
  agent: LockedEdge,
} satisfies Record<string, ComponentType<EdgeProps<CanvasFlowEdge>>>;
