// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  memo,
  useCallback,
  useContext,
  useMemo,
  type ContextType,
  type ReactNode,
} from 'react';
import { DialogLayerProvider } from '@/components/ui/dialog';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { RightPanelProvider } from '@/contexts/RightPanelContext';
import { DAGContext } from '@/features/dags/contexts/DAGContext';
import { useClient } from '@/hooks/api';
import { WorkflowBar } from './bar/WorkflowBar';
import { RunStartProvider } from './bar/RunStartContext';
import { useShowRun } from './bar/useShowRun';
import { WorkspaceCanvas } from './canvas/WorkspaceCanvas';
import { WorkspaceEditorProvider } from './canvas/WorkspaceEditorContext';
import { WorkflowCommands } from './commands/WorkflowCommands';
import type { WorkflowWorkspaceProps } from './contracts';
import {
  useRunHistory,
  useWorkspaceRefresh,
  WorkspaceDataProvider,
} from './data';
import { DockProvider, useDockCommands } from './dock/DockContext';
import { LogsDock } from './dock/LogsDock';
import { RunLinksHost } from './hosts/RunLinksHost';
import { StepLinksHost } from './hosts/StepLinksHost';
import { WorkspaceRunHosts } from './hosts/WorkspaceRunHosts';
import { useWorkspaceKeys } from './keys/useWorkspaceKeys';
import { WorkspaceLayout } from './layout/WorkspaceLayout';
import { EditorLeaveGuard } from './settings/EditorLeaveGuard';
import { SettingsLeaveGuardProvider } from './settings/useSettingsLeaveGuard';
import { useLegacyHistoryRedirect } from './state/useLegacyHistoryRedirect';

type Enqueue = NonNullable<ContextType<typeof DAGContext>['onEnqueue']>;

/**
 * The start request of the Start dialog. A run started from the workspace is
 * the run of the user in this tab: the dock records it, and the request shows
 * the new run as soon as its id is known. The enqueue handler of the page, if
 * any, does the request.
 */
function useStartThenShow(
  fileName: string,
  parentEnqueue: Enqueue | undefined
): Enqueue {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const showRun = useShowRun();
  const { noteUserRun } = useDockCommands();
  return useCallback(
    async (params, dagRunId, immediate, profile, noReuse) => {
      if (parentEnqueue) {
        const result = await parentEnqueue(
          params,
          dagRunId,
          immediate,
          profile,
          noReuse
        );
        const id = typeof result === 'string' && result ? result : dagRunId;
        if (id) {
          noteUserRun?.(id);
          showRun(id);
        }
        return result;
      }
      const body: {
        params: string;
        dagRunId?: string;
        profile?: string;
        noReuse?: boolean;
      } = { params };
      if (dagRunId) body.dagRunId = dagRunId;
      if (profile !== undefined) body.profile = profile;
      if (noReuse !== undefined) body.noReuse = noReuse;
      const init = {
        params: { path: { fileName }, query: { remoteNode } },
        body,
      };
      const { data, error } = await (immediate
        ? client.POST('/dags/{fileName}/start', init)
        : client.POST('/dags/{fileName}/enqueue', init));
      if (error) {
        throw new Error(error.message || 'Failed to start DAG execution.');
      }
      if (data?.dagRunId) {
        noteUserRun?.(data.dagRunId);
        showRun(data.dagRunId);
      }
      return data?.dagRunId;
    },
    [parentEnqueue, client, remoteNode, fileName, showRun, noteUserRun]
  );
}

/**
 * Gives the page's DAGContext the workspace refresh and the DAG name. The run
 * controls of the page (autoOpenStartModal, forceEnqueue) stay. Its onEnqueue
 * starts the run and shows it.
 */
function WorkspaceDAGContext({
  fileName,
  name,
  children,
}: {
  fileName: string;
  name: string;
  children: ReactNode;
}) {
  const parent = useContext(DAGContext);
  const refresh = useWorkspaceRefresh();
  const onEnqueue = useStartThenShow(fileName, parent.onEnqueue);
  const value = useMemo(
    () => ({
      refresh,
      fileName,
      name,
      autoOpenStartModal: parent.autoOpenStartModal,
      forceEnqueue: parent.forceEnqueue,
      onEnqueue,
    }),
    [
      refresh,
      fileName,
      name,
      parent.autoOpenStartModal,
      parent.forceEnqueue,
      onEnqueue,
    ]
  );
  return <DAGContext.Provider value={value}>{children}</DAGContext.Provider>;
}

function WorkspaceKeys() {
  useWorkspaceKeys();
  return null;
}

/**
 * Resolves a legacy /history?idx=n or /executions?idx=n link to its run. The
 * history is read only while such a link is open (the component is mounted
 * then).
 */
function LegacyHistoryRedirect() {
  useLegacyHistoryRedirect(useRunHistory({ enabled: true }));
  return null;
}

/**
 * Body of the workflow page in workspace mode: bar, canvas and dock. One pane
 * shows the workflow and the selected run (?run). It is memoized: the page
 * renders for each event of its SSE feed, and the workspace follows only a
 * change of its props.
 */
export const WorkflowWorkspace = memo(function WorkflowWorkspace({
  fileName,
  dagData,
  refreshDag,
  workspaceName,
  legacyHistoryIdx,
}: WorkflowWorkspaceProps) {
  const dag = dagData.dag;

  if (!dag) return null;

  return (
    // Every shared dialog of the workspace renders above the node view.
    <DialogLayerProvider value="raised">
      <WorkspaceDataProvider
        fileName={fileName}
        dagData={dagData}
        refreshDag={refreshDag}
        workspaceName={workspaceName}
      >
        <DockProvider>
          <RightPanelProvider>
            <WorkspaceDAGContext fileName={fileName} name={dag.name}>
              <WorkspaceRunHosts>
                <RunLinksHost>
                  <WorkspaceEditorProvider>
                    <SettingsLeaveGuardProvider>
                      <EditorLeaveGuard />
                      <RunStartProvider>
                        <WorkspaceLayout
                          bar={
                            <WorkflowBar
                              fileName={fileName}
                              dagData={dagData}
                              workspaceName={workspaceName}
                            />
                          }
                          main={
                            <div
                              data-testid="ww-pane"
                              className="flex h-full min-h-0 w-full min-w-0 flex-1 flex-col"
                            >
                              <StepLinksHost>
                                <WorkspaceCanvas
                                  fileName={fileName}
                                  dagData={dagData}
                                  workspaceName={workspaceName}
                                />
                              </StepLinksHost>
                            </div>
                          }
                          // The dock is always mounted and shows the selected run.
                          dock={<LogsDock fileName={fileName} dag={dag} />}
                        />
                        <WorkflowCommands fileName={fileName} dag={dag} />
                        <WorkspaceKeys />
                        {legacyHistoryIdx !== null && <LegacyHistoryRedirect />}
                      </RunStartProvider>
                    </SettingsLeaveGuardProvider>
                  </WorkspaceEditorProvider>
                </RunLinksHost>
              </WorkspaceRunHosts>
            </WorkspaceDAGContext>
          </RightPanelProvider>
        </DockProvider>
      </WorkspaceDataProvider>
    </DialogLayerProvider>
  );
});
