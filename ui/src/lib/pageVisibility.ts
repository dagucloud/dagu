// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** The attribute that `global.css` reads to pause the animations of a hidden tab. */
export const PAGE_HIDDEN_ATTRIBUTE = 'data-page-hidden';

/**
 * Keeps `data-page-hidden` on the root element equal to the visibility of the
 * tab, so that CSS can pause infinite animations while nobody sees them.
 * Returns a function that stops the watch.
 */
export function watchPageVisibility(doc: Document = document): () => void {
  const root = doc.documentElement;
  const sync = () => {
    if (doc.visibilityState === 'hidden') {
      root.setAttribute(PAGE_HIDDEN_ATTRIBUTE, '');
    } else {
      root.removeAttribute(PAGE_HIDDEN_ATTRIBUTE);
    }
  };
  sync();
  doc.addEventListener('visibilitychange', sync);
  return () => {
    doc.removeEventListener('visibilitychange', sync);
    root.removeAttribute(PAGE_HIDDEN_ATTRIBUTE);
  };
}
