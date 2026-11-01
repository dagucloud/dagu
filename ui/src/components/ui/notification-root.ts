// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

const ROOT_ID = 'notification-root';

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
    'pointer-events-none fixed bottom-4 right-4 z-[90] flex max-h-[calc(100dvh-2rem)] w-[min(24rem,calc(100vw-2rem))] flex-col-reverse gap-2 overflow-y-auto';
  document.body.appendChild(root);
  return root;
}
