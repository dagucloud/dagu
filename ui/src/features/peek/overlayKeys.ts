// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isTextEntryTarget } from '@/features/visual-editor/canvas/useCanvasKeymap';
import { ESC_OWNER_SELECTOR } from '@/features/workflow-workspace/canvas/escFree';

/** Controls whose arrow keys move within them. */
const OWN_ARROWS_SELECTOR = [
  '[role="tablist"]',
  '[role="listbox"]',
  '[role="grid"]',
  '[role="tree"]',
  '[role="menu"]',
  '[role="radiogroup"]',
  '[role="slider"]',
  '[role="separator"]',
  'select',
].join(', ');

/** True when the element or a parent of it scrolls up and down. */
function inScrollingPane(element: Element): boolean {
  for (
    let node: Element | null = element;
    node && node !== document.body && node !== document.documentElement;
    node = node.parentElement
  ) {
    if (node.scrollHeight <= node.clientHeight) continue;
    const overflow = window.getComputedStyle(node).overflowY;
    if (overflow === 'auto' || overflow === 'scroll') return true;
  }
  return false;
}

/**
 * The row that an Up or Down key of the overlay steps to: -1, 1, or null
 * when the key belongs to the page. The page keeps the arrows that it took,
 * those of a text field, a menu, a list, tabs and a pane that scrolls, and
 * all keys while a dialog or a menu is open.
 */
export function overlayStepOfKey(event: KeyboardEvent): -1 | 1 | null {
  if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return null;
  if (
    event.defaultPrevented ||
    event.altKey ||
    event.ctrlKey ||
    event.metaKey ||
    event.shiftKey
  ) {
    return null;
  }
  const target = event.target;
  if (isTextEntryTarget(target)) return null;
  if (
    target instanceof Element &&
    (target.closest(OWN_ARROWS_SELECTOR) || inScrollingPane(target))
  ) {
    return null;
  }
  if (document.querySelector(ESC_OWNER_SELECTOR)) return null;
  return event.key === 'ArrowUp' ? -1 : 1;
}
