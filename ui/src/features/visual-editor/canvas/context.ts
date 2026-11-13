// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext, useContext } from 'react';
import type {
  CanvasCommand,
  CanvasDirection,
  DagEditOp,
  StepCommandGates,
  StepKey,
  ToolSelection,
} from '../contracts';

/**
 * Actions that node and edge components of the canvas call. `VisualCanvas`
 * gives the value. A node that edits its own step (the Model node) calls
 * `onEdit` with the operation that the form of the step would apply.
 */
export interface CanvasActions {
  direction: CanvasDirection;
  /** False when the canvas is frozen or read-only: hide all edit controls. */
  editable: boolean;
  /** True for a view that only reads (a run); the hover toolbar has no Run step. */
  readOnly?: boolean;
  /** True when step cards show only their icon (names, ids and actions hidden). */
  compact?: boolean;
  onCommand(cmd: CanvasCommand): void;
  /**
   * Applies an edit that a node makes on its step, as the form does: the Model
   * node writes the endpoint and the model of its step. It does nothing while
   * the canvas is frozen or read-only. A node still never edits the YAML text.
   */
  onEdit?(op: DagEditOp): void;
  /** The DAG that holds the steps, for the endpoints that its steps may use. */
  dag?: { name: string; workspace?: string | undefined };
  /** Delete the step and connect its parents to its children. */
  onDeleteStep(step: StepKey): void;
  /** Open the palette to insert a step on the edge with this id. */
  onInsertOnEdge(edgeId: string): void;
  /** Gates of the run items; the run items are hidden without them. */
  stepCommandGates?: StepCommandGates;
  /** VisualCanvas always gives it; absent reads as no paste. */
  clipboard?: { canPaste: boolean };
  /** Start the rename of the step. Absent when the canvas cannot edit. */
  onRename?(step: StepKey): void;
  /** The tool entry whose panel is open: its row on the Tools card is lit. */
  selectedTool?: ToolSelection | null;
}

const noop = () => {};

export const CanvasActionsContext = createContext<CanvasActions>({
  direction: 'LR',
  editable: false,
  onCommand: noop,
  onDeleteStep: noop,
  onInsertOnEdge: noop,
  clipboard: { canPaste: false },
});

export function useCanvasActions(): CanvasActions {
  return useContext(CanvasActionsContext);
}
