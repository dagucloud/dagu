// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { components, Stream } from '../../../api/v1/schema';
import { AppBarContext } from '../../../contexts/AppBarContext';
import { RemoteNodeProvider } from '../../../contexts/RemoteNodeContext';
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
import {
  hasWorkspaceLabel,
  sanitizeWorkspaceName,
  sanitizeWorkspaceSelection,
  WorkspaceKind,
  workspaceNameFromLabels,
} from '../../../lib/workspace';
import { I18nText } from '@/i18n/I18nText';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { useI18n } from '@/i18n/I18nProvider';
import {
  LoadErrorState,
  NotFoundState,
  PageLoading,
} from '@/components/PageStates';
import { MonitoringOffer } from '@/components/MonitoringOffer';
import { isNotFoundQueryError } from '@/lib/queryErrors';
import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';

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
  const [searchParams] = useSearchParams();
  const { ts } = useI18n();

  const dagRunId = searchParams.get('dagRunId');
  const stepName = searchParams.get('step');
  const logStream =
    searchParams.get('stream') === Stream.stderr
      ? Stream.stderr
      : Stream.stdout;
  const subDAGRunId = searchParams.get('subDAGRunId');
  const queriedDAGRunName = searchParams.get('dagRunName');
  const queryRemoteNode = searchParams.get('remoteNode')?.trim();
  const appBarRemoteNode = appBarContext.selectedRemoteNode?.trim();
  const remoteNode = queryRemoteNode || appBarRemoteNode || 'local';
  const preserveRemoteNode =
    searchParams.has('remoteNode') || remoteNode !== 'local';
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

  const dagSSE = useDAGSSE(fileName, !!fileName, remoteNode);
  const { setTitle } = appBarContext;

  // Determine active tab
  const tab =
    params.tab === 'docs'
      ? 'wiki'
      : params.tab === 'history'
        ? 'status'
        : params.tab || 'status';

  // Build URL with remote node parameter if needed
  const buildUrl = useCallback(
    (path: string) => {
      const [pathname = '', existingQuery = ''] = path.split('?');
      const nextSearchParams = new URLSearchParams(existingQuery);
      if (preserveRemoteNode) {
        nextSearchParams.set('remoteNode', remoteNode);
      }
      if (queryWorkspace) {
        nextSearchParams.set('workspace', queryWorkspace);
      }
      const query = nextSearchParams.toString();
      return query ? `${pathname}?${query}` : pathname;
    },
    [preserveRemoteNode, queryWorkspace, remoteNode]
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

  useEffect(() => {
    if (params.tab === 'docs' && fileName) {
      navigate(buildUrl(`/dags/${fileName}/wiki`), { replace: true });
    }
    // The history is part of the overview now; keep old links working.
    // The run that the old tab had picked moves to ?run=.
    if (params.tab === 'history' && fileName) {
      const target = new URLSearchParams();
      if (dagRunId) {
        target.set('run', dagRunId);
      }
      const query = target.toString();
      navigate(buildUrl(`/dags/${fileName}${query ? `?${query}` : ''}`), {
        replace: true,
      });
    }
  }, [buildUrl, dagRunId, fileName, navigate, params.tab]);

  // Fetch DAG details — SWR is the single source of truth, refreshed by live invalidations
  const {
    data: dagData,
    error: dagError,
    mutate: mutateDag,
  } = useQuery(
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
  const dagTitle = dagData?.dag?.name || fileName;
  useEffect(() => {
    setTitle(dagTitle);
  }, [dagTitle, setTitle]);

  // A run is selected on the run page. Links that name a run on the
  // workflow's Overview, from older versions or bookmarks, redirect there.
  const runRootName = queriedDAGRunName || dagData?.dag?.name;
  useEffect(() => {
    // The old History tab maps to Overview, and its redirect keeps the run as
    // ?run=, so this redirect must leave it alone.
    if (params.tab === 'history') return;
    if (tab !== 'status' || !dagRunId || !runRootName) return;
    navigate(
      buildDAGRunPageURL({
        rootDAGRunName: runRootName,
        rootDAGRunId: dagRunId,
        subDAGRunId: subDAGRunId ?? undefined,
        remoteNode,
        step: stepName ?? undefined,
      }),
      { replace: true }
    );
  }, [
    dagRunId,
    navigate,
    params.tab,
    remoteNode,
    runRootName,
    stepName,
    subDAGRunId,
    tab,
  ]);

  const dagLabels = [
    ...(dagData?.dag?.labels ?? []),
    ...(dagData?.dag?.tags ?? []),
  ];
  // Scheduled runs happen unattended, which is when alerts matter.
  const runsOnSchedule = !!dagData?.dag?.schedule?.length && !dagData.suspended;
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
  const effectiveDAGRunId = dagRunId;
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
      const details = dagRunResponse?.dagRunDetails;
      return details?.dagRunId === effectiveDAGRunId ? details : undefined;
    }
    return dagData?.latestDAGRun;
  }
  const currentDAGRun = getCurrentDAGRun();
  // The run that the run picker of the overview selected, shown in the header
  // too, so the actions of the header apply to that run.
  const [pickedRun, setPickedRun] = useState<
    components['schemas']['DAGRunDetails'] | undefined
  >();

  // Root DAG-run context state for header display
  const [rootDAGRunData, setRootDAGRunData] = useState<
    DAGRunDetails | undefined
  >(undefined);

  // Refresh all relevant data based on current view. The returned promise
  // settles when the reads finish, so a refresh button can show progress.
  const refreshData = useCallback(async () => {
    await Promise.all([
      mutateDag(),
      subDAGRunId
        ? mutateSubDagRun()
        : effectiveDAGRunId
          ? mutateDagRun()
          : undefined,
    ]);
  }, [
    mutateDag,
    mutateDagRun,
    mutateSubDagRun,
    effectiveDAGRunId,
    subDAGRunId,
  ]);

  const displayDAGRun = currentDAGRun || dagData?.latestDAGRun;
  const displayDAGRunId =
    currentDAGRun?.dagRunId || displayDAGRun?.dagRunId || 'latest';

  // The header reads the root run from this context, so it shows the run that
  // the overview shows.
  const shownDAGRun = pickedRun ?? displayDAGRun;
  useEffect(() => {
    setRootDAGRunData(shownDAGRun);
  }, [shownDAGRun]);

  return (
    <UnsavedChangesProvider>
      <RemoteNodeProvider remoteNode={remoteNode}>
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
            <div className="flex h-full min-h-0 w-full min-w-0 flex-col">
              {!dagData?.dag &&
                (dagError ? (
                  isNotFoundQueryError(dagError) ? (
                    <NotFoundState
                      title={ts('Workflow not found')}
                      description={
                        <I18nTemplate
                          text="No workflow named {name} exists. It may have been renamed or deleted."
                          values={{ name: <code>{fileName}</code> }}
                        />
                      }
                      action={{
                        to: buildUrl('/dags'),
                        label: ts('Back to Workflows'),
                      }}
                    />
                  ) : (
                    <LoadErrorState
                      title={ts("Couldn't load this workflow")}
                      error={dagError}
                      onRetry={() => void mutateDag()}
                    />
                  )
                ) : (
                  <PageLoading />
                ))}
              {dagData?.dag && dagMatchesWorkspace && (
                <>
                  <DAGHeader
                    dag={dagData.dag}
                    currentDAGRun={shownDAGRun}
                    fileName={fileName}
                    refreshFn={refreshData}
                    buildScopedUrl={buildUrl}
                    pinned={dagData.pinned}
                  />
                  {tab === 'status' && runsOnSchedule && (
                    <MonitoringOffer className="mt-3" />
                  )}
                  <div className="min-h-0 flex-1">
                    <DAGDetailsContent
                      fileName={fileName}
                      dag={dagData.dag}
                      currentDAGRun={displayDAGRun}
                      refreshFn={refreshData}
                      activeTab={tab}
                      onTabChange={handleTabChange}
                      dagRunId={displayDAGRunId}
                      stepName={stepName}
                      logStream={logStream}
                      isModal={false}
                      skipHeader={true}
                      onPickedRunChange={setPickedRun}
                      localDags={dagData?.localDags}
                      editorHints={dagData?.editorHints}
                      buildScopedUrl={buildUrl}
                      fillHeight
                    />
                  </div>
                </>
              )}
              {dagData?.dag && !dagMatchesWorkspace && (
                <div className="p-6 text-sm text-muted-foreground">
                  <I18nText
                    text={'This workflow is not in the selected workspace.'}
                  />
                </div>
              )}
            </div>
          </RootDAGRunContext.Provider>
        </DAGContext.Provider>
      </RemoteNodeProvider>
    </UnsavedChangesProvider>
  );
}

export default DAGDetails;
