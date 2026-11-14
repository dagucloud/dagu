// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext } from 'react';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  workspaceNameForSelection,
  workspaceSelectionKey,
} from '@/lib/workspace';

export function useCockpitState() {
  const appBarContext = useContext(AppBarContext);
  const selectedWorkspace = workspaceNameForSelection(
    appBarContext.workspaceSelection
  );
  const workspaceKey = workspaceSelectionKey(appBarContext.workspaceSelection);

  return { selectedWorkspace, workspaceKey };
}
