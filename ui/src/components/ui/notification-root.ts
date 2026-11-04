// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

const ROOT_ID = 'notification-root';
/** The room in px that a panel along the bottom of the screen takes. */
const BOTTOM_INSET_VAR = '--notification-bottom-inset';

/**
 * The one corner of the screen where the notifications stack: the bottom
 * right, over the page. The run progress cards and the toasts draw into it, so
 * that they never cover each other. It is made once and is empty when no
 * notification shows.
 */
export function getNotificationRoot(): HTMLElement {
  const existing = document.getElementById(ROOT_ID);
  if (existing) return existing;
  const root = document.createElement('div');
  root.id = ROOT_ID;
  root.dataset.testid = 'notification-root';
  root.className =
    'pointer-events-none fixed bottom-[calc(1rem+var(--notification-bottom-inset,0px))] right-4 z-[90] flex max-h-[calc(100dvh-2rem-var(--notification-bottom-inset,0px))] w-[min(24rem,calc(100vw-2rem))] flex-col-reverse gap-2 overflow-y-auto';
  document.body.appendChild(root);
  return root;
}

/**
 * Lifts the notifications above a panel that covers the bottom `px` of the
 * screen, so that they do not hide it; 0 puts them back in the corner.
 */
export function setNotificationBottomInset(px: number): void {
  const root = getNotificationRoot();
  if (px > 0) root.style.setProperty(BOTTOM_INSET_VAR, `${Math.round(px)}px`);
  else root.style.removeProperty(BOTTOM_INSET_VAR);
}
