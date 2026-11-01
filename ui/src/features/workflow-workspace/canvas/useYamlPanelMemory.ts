// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef } from 'react';
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
 * Keeps the YAML panel of the workflow page as the user left it. A workflow
 * whose URL opens no other view shows the panel, once the editor can open
 * it, when the panel was open last. Each later change is stored: the panel
 * is open, or no view shows.
 */
export function useYamlPanelMemory({
  fileName,
  ndv,
  restorable,
  openPanel,
}: {
  fileName: string;
  /** ?ndv: the YAML panel ('docked'), the node view of a step, or none. */
  ndv: WorkspaceUrlState['ndv'];
  /** False when the URL opens another view, such as a step or a panel. */
  restorable: boolean;
  /** Opens the YAML panel; null until the editor has loaded. */
  openPanel: (() => void) | null;
}): void {
  // The workflow that the stored state was applied to.
  const appliedTo = useRef<string | null>(null);
  useEffect(() => {
    if (openPanel === null || appliedTo.current === fileName) return;
    appliedTo.current = fileName;
    if (restorable && wasOpen()) openPanel();
  }, [fileName, openPanel, restorable]);

  const seen = useRef({ fileName, ndv });
  useEffect(() => {
    const previous = seen.current;
    seen.current = { fileName, ndv };
    // Another workflow comes with its own URL: no choice of the user.
    if (previous.fileName !== fileName || previous.ndv === ndv) return;
    if (ndv === 'docked') writeLocalStorage(YAML_PANEL_OPEN_KEY, 'true');
    else if (ndv === null) writeLocalStorage(YAML_PANEL_OPEN_KEY, 'false');
  }, [fileName, ndv]);
}
