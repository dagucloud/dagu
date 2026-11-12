// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Position } from './geometry';

export interface XY {
  x: number;
  y: number;
}

/** The pan and zoom of the canvas: where the origin of the flow is on screen. */
export interface Viewport extends XY {
  zoom: number;
}

/** A node of the flow. The size is fixed by the caller, never measured. */
export interface FlowNode<
  D extends Record<string, unknown> = Record<string, unknown>,
  T extends string = string,
> {
  id: string;
  type: T;
  /** Top-left corner, in flow coordinates. */
  position: XY;
  width: number;
  height: number;
  data: D;
  selected?: boolean;
}

export interface FlowEdge<
  D extends Record<string, unknown> = Record<string, unknown>,
  T extends string = string,
> {
  id: string;
  source: string;
  target: string;
  type: T;
  sourceHandle?: string | null;
  targetHandle?: string | null;
  data: D;
  selected?: boolean;
}

/** A link that the user drew from a source handle to a target handle. */
export interface Connection {
  source: string;
  target: string;
  sourceHandle: string | null;
  targetHandle: string | null;
}

/** How a drag from a handle ended. */
export interface ConnectionEndState {
  /** True when the drop made a valid connection, null when it hit no handle. */
  isValid: boolean | null;
  fromNode: { id: string } | null;
  fromHandle: { type: HandleType; id: string | null } | null;
  /** The node of the handle where the drag ended, or null on empty space. */
  toNode: { id: string } | null;
}

export type HandleType = 'source' | 'target';

/** Props that the flow gives to the component of a node. */
export interface NodeProps<N extends FlowNode = FlowNode> {
  id: string;
  data: N['data'];
  selected: boolean;
  /** False when the flow does not let the user draw links. */
  isConnectable: boolean;
}

/** Props that the flow gives to the component of an edge. */
export interface EdgeProps<E extends FlowEdge = FlowEdge> {
  id: string;
  data: E['data'];
  selected: boolean;
  sourceX: number;
  sourceY: number;
  targetX: number;
  targetY: number;
  sourcePosition: Position;
  targetPosition: Position;
  /** Value for the `marker-end` attribute of the path. */
  markerEnd?: string;
}
