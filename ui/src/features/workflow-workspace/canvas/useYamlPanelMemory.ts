// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef, useState } from 'react';
import { writeLocalStorage } from '@/lib/local-storage-migration';
import type { RightPanelId } from '@/contexts/RightPanelContext';
import type { WorkspaceUrlState } from '../state/workspaceUrl';

/** Storage key of the YAML panel of the workflow page: 'true' while it is open. */
export const YAML_PANEL_OPEN_KEY = 'dagu.workspace.yamlPanelOpen';

/** True when the YAML panel was open when the user left the last workflow. */
export function yamlPanelWasOpen(): boolean {
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
    restoring = restorable && yamlPanelWasOpen();
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

/** Storage key of the Workflow panel of the workflow page: 'true' while it is open. */
export const WORKFLOW_PANEL_OPEN_KEY = 'dagu.workspace.workflowPanelOpen';

/** True when the Workflow panel was open when the user left the last workflow. */
export function workflowPanelWasOpen(): boolean {
  try {
    return window.localStorage.getItem(WORKFLOW_PANEL_OPEN_KEY) === 'true';
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
    return false;
  }
}

/**
 * Keeps the Workflow panel of the workflow page as the user left it, as
 * useYamlPanelMemory keeps the YAML panel. `open` tells whether the editor
 * opens the panel at its first render. `pending` is true until a panel has
 * taken the right of the zone, so that the Run panel yields to the Workflow
 * panel before it mounts. Each later open and close of the panel is stored.
 */
export function useWorkflowPanelMemory({
  fileName,
  activePanel,
  restorable,
}: {
  fileName: string;
  /** The right panel that shows (useActiveRightPanel). */
  activePanel: RightPanelId | null;
  /** False when the URL opens another view, or the YAML panel shows. */
  restorable: boolean;
}): { open: boolean; pending: boolean } {
  const [decided, setDecided] = useState<{
    fileName: string;
    open: boolean;
    shown: boolean;
  } | null>(null);
  let current = decided;
  if (current?.fileName !== fileName) {
    current = {
      fileName,
      open: restorable && workflowPanelWasOpen(),
      shown: false,
    };
    setDecided(current);
  } else if (current.open && !current.shown && activePanel !== null) {
    current = { ...current, shown: true };
    setDecided(current);
  }

  const seen = useRef({ fileName, activePanel });
  useEffect(() => {
    const previous = seen.current;
    seen.current = { fileName, activePanel };
    // Another workflow comes with its own panels: no choice of the user.
    if (previous.fileName !== fileName) return;
    if (activePanel === 'workflow' && previous.activePanel !== 'workflow') {
      writeLocalStorage(WORKFLOW_PANEL_OPEN_KEY, 'true');
    } else if (
      previous.activePanel === 'workflow' &&
      activePanel !== 'workflow'
    ) {
      writeLocalStorage(WORKFLOW_PANEL_OPEN_KEY, 'false');
    }
  }, [fileName, activePanel]);

  return { open: current.open, pending: current.open && !current.shown };
}
