// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isTextEntryTarget } from '@/features/visual-editor/canvas/useCanvasKeymap';

/** Open layers that own Esc themselves (Radix dialogs and menus). */
export const ESC_OWNER_SELECTOR = [
  '[role="dialog"][data-state="open"]',
  '[role="alertdialog"][data-state="open"]',
  '[role="menu"]',
].join(', ');

/**
 * Tells if an Esc press is left to the workspace layers: no handler took
 * it, no dialog or menu is open and the focus does not take text.
 */
export function escFree(event: KeyboardEvent): boolean {
  return (
    event.key === 'Escape' &&
    !event.defaultPrevented &&
    !isTextEntryTarget(event.target) &&
    document.querySelector(ESC_OWNER_SELECTOR) === null
  );
}
