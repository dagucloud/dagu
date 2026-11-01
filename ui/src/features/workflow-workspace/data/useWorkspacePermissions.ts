// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import {
  useCanExecute,
  useCanExecuteForWorkspace,
  useCanManageNotifications,
  useCanManageProfiles,
  useCanManageWebhooks,
  useCanWriteForWorkspace,
  useIsAdmin,
} from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useWorkspaceData } from './context';
import type { WorkspacePermissions } from './types';

/** Each gate is today's gate of the action; no gate is merged into another. */
export function useWorkspacePermissions(): WorkspacePermissions {
  const { workspaceName, remoteNode } = useWorkspaceData(
    'useWorkspacePermissions'
  );
  const config = useConfig();
  const canWrite = useCanWriteForWorkspace(workspaceName);
  const canRunDags = config.permissions.runDags;
  const canExecute = useCanExecute();
  const canExecuteForWorkspace = useCanExecuteForWorkspace(workspaceName);
  const isAdmin = useIsAdmin();
  const canManageProfiles = useCanManageProfiles();
  // The gate reads the local server only. A remote node can refuse the
  // webhook read with a 401, which signs the user out of the local server.
  const canManageWebhooks = useCanManageWebhooks() && remoteNode === 'local';
  const canManageNotifications = useCanManageNotifications();
  return useMemo(
    () => ({
      canWrite,
      canRunDags,
      canExecute,
      canExecuteForWorkspace,
      isAdmin,
      canManageProfiles,
      canManageWebhooks,
      canManageNotifications,
    }),
    [
      canWrite,
      canRunDags,
      canExecute,
      canExecuteForWorkspace,
      isAdmin,
      canManageProfiles,
      canManageWebhooks,
      canManageNotifications,
    ]
  );
}
