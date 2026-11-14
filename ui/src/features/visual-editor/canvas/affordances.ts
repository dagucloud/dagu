// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext, useContext } from 'react';
import type { PaletteTarget } from '../contracts';

/**
 * Add-and-connect actions of the nodes and edges: the "+" stalks, the trash
 * of an edge and the tile of an empty canvas. `VisualCanvas` gives them only
 * while the canvas can edit.
 */
export interface CanvasAffordances {
  /** Open the list of steps for a new step at `target`. */
  openPalette(target: PaletteTarget): void;
  /** Remove the link of the edge with this id. A locked edge does nothing. */
  deleteEdge(edgeId: string): void;
  /** Write again the explicit link that an inferred edge stands for. */
  restoreEdge(edgeId: string): void;
}

/** Null when the canvas is read-only or frozen: show no edit affordance. */
export const CanvasAffordancesContext = createContext<CanvasAffordances | null>(
  null
);

export function useCanvasAffordances(): CanvasAffordances | null {
  return useContext(CanvasAffordancesContext);
}
