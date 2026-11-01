// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useMemo, useRef, type ReactNode } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { WorkspaceDataContext, type WorkspaceDataValue } from './context';
import type { ApiDAGRunDetails, DAGPageData, WorkspaceRunValue } from './types';
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

  // The history queries have two SWR keys (with and without the test runs):
  // a refresh reads all of them again, as SWR merges the calls of one key.
  const historyMutators = useRef(new Set<() => Promise<unknown>>());
  const historyLists = useRef(new Set<() => readonly ApiDAGRunDetails[]>());
  const registerHistory = useCallback(
    (
      mutate: () => Promise<unknown>,
      runs?: () => readonly ApiDAGRunDetails[]
    ) => {
      historyMutators.current.add(mutate);
      if (runs) historyLists.current.add(runs);
      return () => {
        historyMutators.current.delete(mutate);
        if (runs) historyLists.current.delete(runs);
      };
    },
    []
  );
  const listedRun = useCallback((runId: string) => {
    for (const runs of historyLists.current) {
      const found = runs().find((run) => run.dagRunId === runId);
      if (found) return found;
    }
    return null;
  }, []);

  const { run: selectedRun, mutatePinned } = useSelectedRun({
    dagData,
    remoteNode,
    listedRun,
  });

  // The page may pass a new function on each render.
  const refreshDagRef = useRef(refreshDag);
  refreshDagRef.current = refreshDag;

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
