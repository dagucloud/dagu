// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useState } from 'react';
import { usePrefersReducedMotion } from '@/hooks/usePrefersReducedMotion';

/**
 * How long the workflow overlay takes to slide in or out. The `duration-200`
 * classes of the overlay match it.
 */
export const OVERLAY_SLIDE_MS = 200;

/**
 * True while a closed overlay slides out, from the close for
 * OVERLAY_SLIDE_MS. `returned` tells whether the close shows the page below
 * again: a close that moves on to another page, such as Open workflow, ends
 * at once, as does every close under reduced motion. An open stops the
 * slide.
 */
export function useOverlayExit(isOpen: boolean, returned: boolean): boolean {
  const reducedMotion = usePrefersReducedMotion();
  const [wasOpen, setWasOpen] = useState(isOpen);
  const [closing, setClosing] = useState(false);
  if (wasOpen !== isOpen) {
    setWasOpen(isOpen);
    setClosing(!isOpen && returned && !reducedMotion);
  }
  useEffect(() => {
    if (!closing) return;
    const timer = window.setTimeout(() => setClosing(false), OVERLAY_SLIDE_MS);
    return () => window.clearTimeout(timer);
  }, [closing]);
  return closing;
}
