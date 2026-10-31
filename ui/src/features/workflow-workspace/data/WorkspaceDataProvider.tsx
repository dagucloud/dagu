// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useMemo, useRef, type ReactNode } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { WorkspaceDataContext, type WorkspaceDataValue } from './context';
import type { DAGPageData, WorkspaceRunValue } from './types';
import { useSelectedRun } from './useWorkspaceRun';
import { settleAll } from './useWorkspaceRefresh';

export interface WorkspaceDataProviderProps {
  fileName: string;
  dagData: DAGPageData;
  refreshDag(): Promise<unknown>;
  workspaceName: string | null;
  children: ReactNode;
}

/** Holds the selected run (from ?run) and the refresh of the workspace. */
export function WorkspaceDataProvider({
  fileName,
  dagData,
  refreshDag,
  workspaceName,
  children,
}: WorkspaceDataProviderProps) {
  const remoteNode = useRemoteNode();
  const { run: selectedRun, mutatePinned } = useSelectedRun({
    dagData,
    remoteNode,
  });

  // The page may pass a new function on each render.
  const refreshDagRef = useRef(refreshDag);
  refreshDagRef.current = refreshDag;

  // The history queries have two SWR keys (with and without the test runs):
  // a refresh reads all of them again, as SWR merges the calls of one key.
  const historyMutators = useRef(new Set<() => Promise<unknown>>());
  const registerHistory = useCallback((mutate: () => Promise<unknown>) => {
    historyMutators.current.add(mutate);
    return () => {
      historyMutators.current.delete(mutate);
    };
  }, []);

  const refresh = useCallback(() => {
    return settleAll([
      () => refreshDagRef.current(),
      mutatePinned,
      ...historyMutators.current,
    ]);
  }, [mutatePinned]);

  const run: WorkspaceRunValue = useMemo(
    () => ({ ...selectedRun, refresh }),
    [selectedRun, refresh]
  );

  const value: WorkspaceDataValue = useMemo(
    () => ({
      fileName,
      dagData,
      workspaceName,
      remoteNode,
      run,
      refresh,
      registerHistory,
    }),
    [
      fileName,
      dagData,
      workspaceName,
      remoteNode,
      run,
      refresh,
      registerHistory,
    ]
  );

  return (
    <WorkspaceDataContext.Provider value={value}>
      {children}
    </WorkspaceDataContext.Provider>
  );
}
