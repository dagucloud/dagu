// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** Stack order of the workspace layers, as Tailwind z-index classes. */
export const Z = {
  canvas: 'z-0',
  canvasOverlay: 'z-10',
  dock: 'z-30',
  ndv: 'z-[60]',
  menu: 'z-[70]',
  dialog: 'z-[80]',
  toast: 'z-[90]',
} as const;
