// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { components } from '../../../api/v1/schema';
import { AppBarContext } from '../../../contexts/AppBarContext';
import { usePageContext } from '../../../contexts/PageContext';
import { UnsavedChangesProvider } from '../../../contexts/UnsavedChangesContext';
import {
  DAGDetailsContent,
  DAGHeader,
} from '../../../features/dags/components/dag-details';
import { DAGContext } from '../../../features/dags/contexts/DAGContext';
import { RootDAGRunContext } from '../../../features/dags/contexts/RootDAGRunContext';
import { useQuery } from '../../../hooks/api';
import { useDAGRunSSE } from '../../../hooks/useDAGRunSSE';
import { useDAGSSE } from '../../../hooks/useDAGSSE';
import { whenEnabled } from '../../../hooks/queryUtils';
import {
  sseFallbackOptions,
  useSSECacheSync,
} from '../../../hooks/useSSECacheSync';
import { useSubDAGRunSSE } from '../../../hooks/useSubDAGRunSSE';
import dayjs from '../../../lib/dayjs';
import {
  hasWorkspaceLabel,
  sanitizeWorkspaceName,
  sanitizeWorkspaceSelection,
  WorkspaceKind,
  workspaceNameFromLabels,
} from '../../../lib/workspace';

type Params = {
  fileName: string;
  name: string;
  tab?: string;
};

type DAGRunDetails = components['schemas']['DAGRunDetails'];

function DAGDetails() {
  const params = useParams<Params>();
  const navigate = useNavigate();
  const appBarContext = useContext(AppBarContext);
  const { setContext } = usePageContext();
  const [searchParams] = useSearchParams();

  const dagRunId = searchParams.get('dagRunId');
  const stepName = searchParams.get('step');
  const subDAGRunId = searchParams.get('subDAGRunId');
  const queriedDAGRunName = searchParams.get('dagRunName');
  const [trackedDagRunId, setTrackedDagRunId] = useState<string>();
  const previousURLDagRunId = useRef<string | null>(dagRunId);
  const remoteNode =
    searchParams.get('remoteNode') ||
    appBarContext.selectedRemoteNode ||
    'local';
  const queryWorkspace = sanitizeWorkspaceName(
    searchParams.get('workspace') ?? ''
  );
  const appWorkspaceSelection = appBarContext.workspaceSelection;
  const workspaceSelection = useMemo(() => {
    if (queryWorkspace) {
      return { kind: WorkspaceKind.workspace, workspace: queryWorkspace };
    }
    return sanitizeWorkspaceSelection(appWorkspaceSelection);
  }, [appWorkspaceSelection, queryWorkspace]);
  const scopedQuery = useMemo(
    () => ({
      remoteNode,
    }),
    [remoteNode]
  );
  const fileName = params.fileName || '';

  // Set page context for agent chat
  useEffect(() => {
    if (fileName) {
      setContext({
        dagFile: fileName,
        dagRunId: dagRunId || undefined,
        source: 'dag-details-page',
      });
    }
    return () => {
      setContext(null);
    };
  }, [fileName, dagRunId, setContext]);

  const dagSSE = useDAGSSE(fileName, !!fileName);

  // Determine active tab
  const tab = params.tab || 'status';

  // Format duration utility function
  const formatDuration = useCallback(
    (startDate: string, endDate: string): string => {
      if (!startDate || !endDate) return '--';

      const duration = dayjs.duration(dayjs(endDate).diff(dayjs(startDate)));
      const hours = Math.floor(duration.asHours());
      const minutes = duration.minutes();
      const seconds = duration.seconds();

      if (hours > 0) return `${hours}h ${minutes}m ${seconds}s`;
      if (minutes > 0) return `${minutes}m ${seconds}s`;
      return `${seconds}s`;
    },
    []
  );

  // Build URL with remote node parameter if needed
  const buildUrl = useCallback(
    (path: string) => {
      const nextSearchParams = new URLSearchParams();
      if (remoteNode && remoteNode !== 'local') {
        nextSearchParams.set('remoteNode', remoteNode);
      }
      if (queryWorkspace) {
        nextSearchParams.set('workspace', queryWorkspace);
      }
      const query = nextSearchParams.toString();
      return query ? `${path}?${query}` : path;
    },
    [queryWorkspace, remoteNode]
  );

  // Handle tab changes - navigates to the appropriate URL for the given tab
  const handleTabChange = useCallback(
    (newTab: string) => {
      if (!fileName) return;
      const path =
        newTab === 'status'
          ? `/dags/${fileName}`
          : `/dags/${fileName}/${newTab}`;
      navigate(buildUrl(path));
    },
    [fileName, navigate, buildUrl]
  );

  // Navigate to status tab - convenience wrapper for handleTabChange
  const navigateToStatusTab = useCallback(() => {
    if (tab !== 'status') {
      handleTabChange('status');
    }
  }, [tab, handleTabChange]);

  // Fetch DAG details — SWR is the single source of truth, refreshed by live invalidations
  const { data: dagData, mutate: mutateDag } = useQuery(
    '/dags/{fileName}',
    {
      params: {
        query: scopedQuery,
        path: { fileName },
      },
    },
    sseFallbackOptions(dagSSE)
  );
  useSSECacheSync(dagSSE, mutateDag);
  const dagLabels = [
    ...(dagData?.dag?.labels ?? []),
    ...(dagData?.dag?.tags ?? []),
  ];
  const dagWorkspaceName = workspaceNameFromLabels(dagLabels);
  const dagHasWorkspaceLabel = hasWorkspaceLabel(dagLabels);
  const dagMatchesWorkspace =
    workspaceSelection.kind === WorkspaceKind.all ||
    (workspaceSelection.kind === WorkspaceKind.default &&
      !dagHasWorkspaceLabel) ||
    (workspaceSelection.kind === WorkspaceKind.workspace &&
      dagWorkspaceName === workspaceSelection.workspace);

  // Use dagRunName from URL if available, otherwise use the name from dagData
  const dagRunName = queriedDAGRunName || dagData?.dag?.name || '';
  const effectiveDAGRunId = trackedDagRunId || dagRunId;
  const dagRunQueryEnabled = Boolean(
    dagRunName && effectiveDAGRunId && !subDAGRunId && dagMatchesWorkspace
  );

  // Fetch specific DAG-run data if dagRunId is provided
  const dagRunSSE = useDAGRunSSE(
    dagRunName,
    effectiveDAGRunId || '',
    dagRunQueryEnabled,
    remoteNode
  );
  const { data: dagRunResponse, mutate: mutateDagRun } = useQuery(
    '/dag-runs/{name}/{dagRunId}',
    whenEnabled(dagRunQueryEnabled, {
      params: {
        path: {
          name: dagRunName,
          dagRunId: effectiveDAGRunId || '',
        },
        query: scopedQuery,
      },
    }),
    sseFallbackOptions(dagRunSSE)
  );
  useSSECacheSync(dagRunSSE, mutateDagRun);

  // Fetch sub DAG-run data if needed
  const subDAGRunQueryEnabled = Boolean(
    subDAGRunId && dagRunId && dagRunName && dagMatchesWorkspace
  );
  const subDAGRunSSE = useSubDAGRunSSE(
    dagRunName,
    dagRunId || '',
    subDAGRunId || '',
    subDAGRunQueryEnabled,
    remoteNode
  );
  const { data: subDAGRunResponse, mutate: mutateSubDagRun } = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}',
    whenEnabled(subDAGRunQueryEnabled, {
      params: {
        path: {
          name: dagRunName,
          dagRunId: dagRunId || '',
          subDAGRunId: subDAGRunId || '',
        },
        query: scopedQuery,
      },
    }),
    sseFallbackOptions(subDAGRunSSE)
  );
  useSSECacheSync(subDAGRunSSE, mutateSubDagRun);

  // Determine the current DAG-run to display based on URL parameters
  function getCurrentDAGRun(): DAGRunDetails | undefined {
    if (subDAGRunId) {
      return subDAGRunResponse?.dagRunDetails;
    }
    if (effectiveDAGRunId) {
      return dagRunResponse?.dagRunDetails;
    }
    return dagData?.latestDAGRun;
  }
  const currentDAGRun = getCurrentDAGRun();

  // Root DAG-run context state for header display
  const [rootDAGRunData, setRootDAGRunData] = useState<
    DAGRunDetails | undefined
  >(undefined);

  // Update root DAG-run data when current DAG-run or latest DAG-run changes
  useEffect(() => {
    const newData = currentDAGRun || dagData?.latestDAGRun;
    if (newData) {
      setRootDAGRunData(newData);
    }
  }, [currentDAGRun, dagData?.latestDAGRun]);

  // Refresh all relevant data based on current view
  const refreshData = useCallback(() => {
    mutateDag();
    if (subDAGRunId) {
      mutateSubDagRun();
    } else if (effectiveDAGRunId) {
      mutateDagRun();
    }
  }, [
    mutateDag,
    mutateDagRun,
    mutateSubDagRun,
    effectiveDAGRunId,
    subDAGRunId,
  ]);

  const handleRunStarted = useCallback(
    (nextDAGRunId: string) => {
      setTrackedDagRunId(nextDAGRunId);
      const nextSearchParams = new URLSearchParams();
      nextSearchParams.set('dagRunId', nextDAGRunId);
      nextSearchParams.set('dagRunName', dagData?.dag?.name || dagRunName);
      if (remoteNode && remoteNode !== 'local') {
        nextSearchParams.set('remoteNode', remoteNode);
      }
      if (queryWorkspace) {
        nextSearchParams.set('workspace', queryWorkspace);
      }
      navigate(`/dags/${fileName}?${nextSearchParams.toString()}`);
      void mutateDag();
    },
    [
      dagData?.dag?.name,
      dagRunName,
      fileName,
      mutateDag,
      navigate,
      queryWorkspace,
      remoteNode,
    ]
  );

  useEffect(() => {
    const previous = previousURLDagRunId.current;
    previousURLDagRunId.current = dagRunId;
    if (!dagRunId || dagRunId === trackedDagRunId) {
      return;
    }
    if (previous !== dagRunId) {
      setTrackedDagRunId(undefined);
    }
  }, [dagRunId, trackedDagRunId]);

  useEffect(() => {
    setTrackedDagRunId(undefined);
  }, [fileName, remoteNode]);

  // Determine which DAG-run to display - fallback to latest when specific run is loading
  const displayDAGRun = currentDAGRun || dagData?.latestDAGRun;

  return (
    <UnsavedChangesProvider>
      <DAGContext.Provider
        value={{
          refresh: refreshData,
          fileName,
          name: dagRunName,
        }}
      >
        <RootDAGRunContext.Provider
          value={{
            data: rootDAGRunData,
            setData: setRootDAGRunData,
          }}
        >
          <div className="flex w-full min-w-0 max-w-7xl flex-col">
            {dagData?.dag && dagMatchesWorkspace && (
              <>
                <DAGHeader
                  dag={dagData.dag}
                  currentDAGRun={displayDAGRun}
                  fileName={fileName}
                  filePath={dagData.filePath}
                  refreshFn={refreshData}
                  formatDuration={formatDuration}
                  navigateToStatusTab={navigateToStatusTab}
                />
                <DAGDetailsContent
                  fileName={fileName}
                  filePath={dagData.filePath}
                  dag={dagData.dag}
                  currentDAGRun={displayDAGRun}
                  refreshFn={refreshData}
                  formatDuration={formatDuration}
                  activeTab={tab}
                  onTabChange={handleTabChange}
                  dagRunId={currentDAGRun?.dagRunId}
                  stepName={stepName}
                  isModal={false}
                  navigateToStatusTab={navigateToStatusTab}
                  skipHeader={true}
                  localDags={dagData?.localDags}
                  editorHints={dagData?.editorHints}
                  onRunStarted={handleRunStarted}
                />
              </>
            )}
            {dagData?.dag && !dagMatchesWorkspace && (
              <div className="p-6 text-sm text-muted-foreground">
                This DAG is not in the selected workspace.
              </div>
            )}
          </div>
        </RootDAGRunContext.Provider>
      </DAGContext.Provider>
    </UnsavedChangesProvider>
  );
}

export default DAGDetails;
