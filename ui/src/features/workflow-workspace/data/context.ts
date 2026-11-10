// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext, useContext } from 'react';
import type { ApiDAGRunDetails, DAGPageData, WorkspaceRunValue } from './types';

export interface WorkspaceDataValue {
  fileName: string;
  dagData: DAGPageData;
  workspaceName: string | null;
  remoteNode: string;
  run: WorkspaceRunValue;
  /**
   * The run that the page shows without its workflow file, from the
   * definition that the run stored; null for a page of a workflow file.
   */
  standaloneRun?: ApiDAGRunDetails | null;
  refresh(): Promise<void>;
  /**
   * Registers the mutator of a mounted, enabled history query, and a reader
   * of the runs that it lists, registered again when they change. Returns
   * the unregister function.
   */
  registerHistory(
    mutate: () => Promise<unknown>,
    runs?: () => readonly ApiDAGRunDetails[]
  ): () => void;
}

export const WorkspaceDataContext = createContext<WorkspaceDataValue | null>(
  null
);

/**
 * The run that the page shows without its workflow file; null for a page of
 * a workflow file, and outside a workspace.
 */
export function useStandaloneRun(): ApiDAGRunDetails | null {
  return useContext(WorkspaceDataContext)?.standaloneRun ?? null;
}

export function useWorkspaceData(hook: string): WorkspaceDataValue {
  const value = useContext(WorkspaceDataContext);
  if (!value) {
    throw new Error(`${hook} must be used within WorkspaceDataProvider`);
  }
  return value;
}
