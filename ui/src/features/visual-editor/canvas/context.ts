// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext, useContext } from 'react';
import type {
  CanvasCommand,
  CanvasDirection,
  StepCommandGates,
  StepKey,
} from '../contracts';

/**
 * Actions that node and edge components of the canvas call. `VisualCanvas`
 * gives the value; the components never emit edit operations themselves.
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
