// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext, useContext } from 'react';

/** What a host puts over the canvas: free space in px that the canvas keeps clear. */
export interface CanvasOverlayInset {
  /** Width of a panel that covers the right of the canvas. */
  right: number;
}

const NONE: CanvasOverlayInset = { right: 0 };

/**
 * A host that opens a panel over the canvas gives the covered width here.
 * The canvas fits its content, and places its toolbar and its overview, in
 * the part that stays free.
 */
export const CanvasOverlayInsetContext =
  createContext<CanvasOverlayInset>(NONE);

export function useCanvasOverlayInset(): CanvasOverlayInset {
  return useContext(CanvasOverlayInsetContext);
}
