// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef, type RefObject } from 'react';
import { escFree } from './escFree';

/**
 * Esc clears the step selection of a graph zone when nothing else owns the
 * key: no panel or node view is open, no dialog or menu is above, the focus
 * takes no text and the canvas did not take the key itself. The key counts
 * when the focus is in the zone or on the page body. A shown canvas gets the
 * key itself; without one, `clear` writes the URL.
 */
export function useEscClearsStep({
  zoneRef,
  active,
  hasStep,
  layerOpen,
  clear,
}: {
  zoneRef: RefObject<HTMLElement | null>;
  /** The pane is the visible one. */
  active: boolean;
  hasStep: boolean;
  /** A right panel or the node view is open: it owns Esc. */
  layerOpen: boolean;
  clear(): void;
}): void {
  const clearRef = useRef(clear);
  clearRef.current = clear;
  useEffect(() => {
    if (!active || !hasStep || layerOpen) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (!escFree(event)) return;
      const target = event.target as Node | null;
      const onPage =
        target === document.body || target === document.documentElement;
      if (!onPage && !(target && zoneRef.current?.contains(target))) return;
      event.preventDefault();
      // The canvas clears its own selection and tells the host: the editor
      // restores a selection that the host clears from outside.
      const canvas = zoneRef.current?.querySelector<HTMLElement>(
        '[data-testid="ve-canvas"]'
      );
      if (canvas && !canvas.closest('[hidden]')) {
        canvas.focus();
        canvas.dispatchEvent(
          new KeyboardEvent('keydown', {
            key: 'Escape',
            bubbles: true,
            cancelable: true,
          })
        );
        return;
      }
      clearRef.current();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [active, hasStep, layerOpen, zoneRef]);
}
