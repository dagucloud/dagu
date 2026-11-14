// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The window widths of the layouts, the ones of Tailwind: a phone below md,
 * a laptop from lg. Class names keep their literal variants (md:, lg:) for
 * the Tailwind scan; code reads these.
 */
export const PHONE_BELOW_PX = 768;
export const LAPTOP_FROM_PX = 1024;

/** A window that is no phone. */
export const WIDE_QUERY = `(min-width: ${PHONE_BELOW_PX}px)`;
/** A window of a laptop or wider. */
export const LAPTOP_QUERY = `(min-width: ${LAPTOP_FROM_PX}px)`;
/**
 * A touch screen: one of its pointers is coarse. Any pointer, as a touch
 * screen laptop has a mouse as its primary pointer.
 */
export const COARSE_POINTER_QUERY = '(any-pointer: coarse)';

/**
 * Whether the media query matches now, outside React. `fallback` is the
 * answer where the browser has no matchMedia, as in tests.
 */
export function matchesQuery(query: string, fallback: boolean): boolean {
  if (typeof window.matchMedia !== 'function') return fallback;
  return window.matchMedia(query)?.matches ?? fallback;
}
