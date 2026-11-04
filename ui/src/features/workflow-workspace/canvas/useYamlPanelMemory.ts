// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef, useState } from 'react';
import { writeLocalStorage } from '@/lib/local-storage-migration';
import type { WorkspaceUrlState } from '../state/workspaceUrl';

/** Storage key of the YAML panel of the workflow page: 'true' while it is open. */
export const YAML_PANEL_OPEN_KEY = 'dagu.workspace.yamlPanelOpen';

function wasOpen(): boolean {
  try {
    return window.localStorage.getItem(YAML_PANEL_OPEN_KEY) === 'true';
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
    return false;
  }
}

/**
 * Keeps the YAML panel of the workflow page as the user left it, and gives
 * the state of ?ndv to show. A workflow whose URL opens no other view shows
 * the panel, from its first render, when the panel was open last; `restore`
 * then writes it into the URL. Each later change is stored: the panel is
 * open, or no view shows.
 */
export function useYamlPanelMemory({
  fileName,
  ndv,
  restorable,
  restore,
}: {
  fileName: string;
  /** ?ndv: the YAML panel ('docked'), the node view of a step, or none. */
  ndv: WorkspaceUrlState['ndv'];
  /** False when the URL opens another view, such as a step or a panel. */
  restorable: boolean;
  /** Writes ?ndv=docked. */
  restore: () => void;
}): WorkspaceUrlState['ndv'] {
  // Decided in the first render of each workflow, so that the editor lays
  // out its graph with the panel: a panel that opened later shrank the graph.
  const [decided, setDecided] = useState<{
    fileName: string;
    open: boolean;
  } | null>(null);
  let restoring = decided?.fileName === fileName && decided.open;
  if (decided?.fileName !== fileName) {
    restoring = restorable && wasOpen();
    setDecided({ fileName, open: restoring });
  } else if (restoring && ndv !== null) {
    // The URL has the state now: a later close is the choice of the user.
    setDecided({ fileName, open: false });
  }
  const pending = restoring && ndv === null;
  const restoreRef = useRef(restore);
  restoreRef.current = restore;
  useEffect(() => {
    if (pending) restoreRef.current();
  }, [pending]);

  const seen = useRef({ fileName, ndv });
  useEffect(() => {
    const previous = seen.current;
    seen.current = { fileName, ndv };
    // Another workflow comes with its own URL: no choice of the user.
    if (previous.fileName !== fileName || previous.ndv === ndv) return;
    if (ndv === 'docked') writeLocalStorage(YAML_PANEL_OPEN_KEY, 'true');
    else if (ndv === null) writeLocalStorage(YAML_PANEL_OPEN_KEY, 'false');
  }, [fileName, ndv]);

  return pending ? 'docked' : ndv;
}
