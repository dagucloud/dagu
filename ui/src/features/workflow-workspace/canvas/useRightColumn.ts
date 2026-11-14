// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useState } from 'react';
import { LAPTOP_QUERY, matchesQuery } from '@/lib/breakpoints';
import { writeLocalStorage } from '@/lib/local-storage-migration';
import {
  WORKFLOW_PANEL_OPEN_KEY,
  YAML_PANEL_OPEN_KEY,
} from './useYamlPanelMemory';

/** Storage key of the right column of the workflow page: 'workflow', 'yaml' or 'none'. */
export const RIGHT_COLUMN_KEY = 'dagu.workspace.rightColumn';

/** The tab that the right column shows. */
export type RightColumnTab = 'workflow' | 'yaml';

/**
 * The tab that the user left the right column on, in any workflow; null when
 * it was collapsed. Before any choice it reads the panels that the page had
 * before the column, and a laptop shows the YAML.
 */
function storedTab(): RightColumnTab | null {
  try {
    const stored = window.localStorage.getItem(RIGHT_COLUMN_KEY);
    if (stored === 'workflow' || stored === 'yaml') return stored;
    if (stored === 'none') return null;
    if (window.localStorage.getItem(WORKFLOW_PANEL_OPEN_KEY) === 'true') {
      return 'workflow';
    }
    const yaml = window.localStorage.getItem(YAML_PANEL_OPEN_KEY);
    if (yaml !== null) return yaml === 'true' ? 'yaml' : null;
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
    return null;
  }
  return matchesQuery(LAPTOP_QUERY, false) ? 'yaml' : null;
}

/**
 * The tab of the right column of the workflow page and its setter; null
 * collapses the column. The choice is stored for every workflow.
 */
export function useRightColumn(): [
  RightColumnTab | null,
  (tab: RightColumnTab | null) => void,
] {
  const [tab, setTabState] = useState(storedTab);
  const setTab = useCallback((next: RightColumnTab | null) => {
    setTabState(next);
    writeLocalStorage(RIGHT_COLUMN_KEY, next ?? 'none');
  }, []);
  return [tab, setTab];
}
