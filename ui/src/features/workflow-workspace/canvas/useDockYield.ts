// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect } from 'react';
import { useDockCommands } from '../dock/DockContext';

/**
 * The expanded node view needs the room of the Run panel: the panel
 * collapses to its bar while `viewOpen` is true, and comes back at its
 * height when the view closes. A canvas click never opens the panel, so the
 * panel always comes back.
 */
export function useDockYield(viewOpen: boolean): void {
  const { yieldToView } = useDockCommands();
  useEffect(() => {
    if (!viewOpen) return;
    yieldToView?.(true, { restore: true });
    return () => yieldToView?.(false);
  }, [viewOpen, yieldToView]);
}
