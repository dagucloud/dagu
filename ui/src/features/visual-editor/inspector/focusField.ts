// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** The controls that a focus request can land on, first in the box first. */
const FOCUSABLE = [
  'input:not([disabled]):not([type="hidden"])',
  'textarea:not([disabled])',
  'select:not([disabled])',
  'button:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
  // The frame of a code field that waits for Monaco: it hands the focus on.
  '[data-monaco-pending]',
].join(',');

/**
 * Focuses the control that marks itself with `data-reveal-focus`, else the
 * first control in the box, else the box itself. A list marks its add control,
 * so that a reveal never lands on a button that deletes; a list with no add
 * control marks the box with `data-reveal-self`, and the box takes the focus.
 */
export function focusBox(box: HTMLElement): void {
  for (let parent = box.parentElement; parent; parent = parent.parentElement) {
    if (parent instanceof HTMLDetailsElement) parent.open = true;
  }
  const target =
    box.querySelector<HTMLElement>('[data-reveal-focus]:not([disabled])') ??
    (box.hasAttribute('data-reveal-self')
      ? box
      : box.querySelector<HTMLElement>(FOCUSABLE)) ??
    box;
  if (target === box && !box.hasAttribute('tabindex')) box.tabIndex = -1;
  target.focus();
  target.scrollIntoView?.({ block: 'nearest' });
}
