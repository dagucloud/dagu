// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  lazy,
  Suspense,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
} from 'react';
import { cn } from '@/lib/utils';
import { WorkspaceLoading } from '../../../features/workflow-workspace/WorkspaceLoading';
import {
  Link,
  Navigate,
  useLocation,
  useParams,
  useSearchParams,
} from 'react-router-dom';
import { components } from '../../../api/v1/schema';
import { AppBarContext } from '../../../contexts/AppBarContext';
import {
  useFullBleed,
  useHideBreadcrumb,
} from '../../../contexts/LayoutChromeContext';
import { RemoteNodeProvider } from '../../../contexts/RemoteNodeContext';
import { UnsavedChangesProvider } from '../../../contexts/UnsavedChangesContext';
import { DAGContext } from '../../../features/dags/contexts/DAGContext';
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
import { LoadErrorState, NotFoundState } from '@/components/PageStates';
import { isNotFoundQueryError } from '@/lib/queryErrors';
import { WorkflowWorkspace } from '@/features/workflow-workspace';
import { LogPopoutPage } from '@/features/workflow-workspace/popout/LogPopoutPage';
import { useLegacyRedirect } from '@/features/workflow-workspace/state/useLegacyRedirect';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import LoadingIndicator from '@/components/ui/loading-indicator';

// The run view of a workflow file that is gone reaches Mermaid, which the
// page keeps out of its static imports.
const DAGRunDetailsContent = lazy(() =>
  import('@/features/dag-runs/components/dag-run-details').then((m) => ({
    default: m.DAGRunDetailsContent,
  }))
);

type Params = {
  fileName: string;
  name: string;
  tab?: string;
};

type DAGRunDetails = components['schemas']['DAGRunDetails'];

/**
 * A run on its own, for a link that no workspace shows: the run of the link,
 * or its sub-run in ?subRun. It follows the run while the run is active.
 */
function StandaloneRun({
  run,
  subRunId,
  remoteNode,
}: {
  run: DAGRunDetails;
  subRunId: string | null;
  remoteNode: string;
}) {
  const sub = subRunId && subRunId !== run.dagRunId ? subRunId : null;
  const live = useBoundedDAGRunDetails({
    target: sub
      ? {
          remoteNode,
          name: run.name,
          dagRunId: sub,
          parentName: run.name,
          parentDAGRunId: run.dagRunId,
          subDAGRunId: sub,
        }
      : { remoteNode, name: run.name, dagRunId: run.dagRunId },
    pollIntervalMs: 2000,
  });
  const fresh =
    live.data?.dagRunId === (sub ?? run.dagRunId) ? live.data : null;
  // A sub-run that cannot load leaves the run of the link.
  const shown = fresh ?? (sub && !live.error ? null : run);
  if (!shown) return <LoadingIndicator />;
  return (
    <Suspense fallback={null}>
      <DAGRunDetailsContent
        name={shown.name}
        dagRun={shown}
        dagRunId={shown.dagRunId}
        refreshFn={() => void live.refresh()}
        fillHeight
      />
    </Suspense>
  );
}

function DAGDetails() {
  const params = useParams<Params>();
  const appBarContext = useContext(AppBarContext);
  const [searchParams] = useSearchParams();
  const { ts } = useI18n();
  // The log tabs are full-page logs of one run.
  const popout = params.tab === 'log' || params.tab === 'dagRun-log';

  const dagRunId = searchParams.get('dagRunId');
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
  const location = useLocation();
  // The same page without the workspace of the URL.
  const allWorkspacesTo = useMemo(() => {
    const query = new URLSearchParams(location.search);
    query.delete('workspace');
    const search = query.toString();
    return `${location.pathname}${search ? `?${search}` : ''}`;
  }, [location.pathname, location.search]);

  const dagSSE = useDAGSSE(fileName, !!fileName, remoteNode);
  const { setTitle } = appBarContext;

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

  // A run link names the workflow by the DAG name of the run, which finds no
  // file, or the file of another DAG, when the DAG name differs from the file
  // name. The run gives its file.
  const linkedRunId = searchParams.get('run');
  const lookUpRunFile =
    Boolean(linkedRunId) &&
    (isNotFoundQueryError(dagError) ||
      (!!dagData?.dag &&
        dagData.dag.name !== fileName.replace(/\.ya?ml$/, '')));
  const { data: linkedRun, error: linkedRunError } = useQuery(
    '/dag-runs/{name}/{dagRunId}',
    whenEnabled(lookUpRunFile, {
      params: {
        path: { name: fileName, dagRunId: linkedRunId || '' },
        query: scopedQuery,
      },
    })
  );
  const linkedRunFile = linkedRun?.dagRunDetails?.sourceFileName;
  const runFileRedirect =
    lookUpRunFile && linkedRunFile && linkedRunFile !== fileName
      ? `/dags/${encodeURIComponent(linkedRunFile)}?${searchParams.toString()}`
      : null;
  const resolvingRunFile =
    lookUpRunFile && !linkedRunError && (!linkedRun || !!runFileRedirect);
  // The run shows on its own where no workspace shows it: its source file is
  // gone (deleted, or renamed with the DAG name kept), or the file now holds
  // the DAG under another name.
  const lookedUpRun = linkedRun?.dagRunDetails ?? null;
  const ownRun =
    lookedUpRun &&
    !runFileRedirect &&
    ((isNotFoundQueryError(dagError) && !linkedRunFile) ||
      (!!dagData?.dag &&
        !!linkedRunFile &&
        lookedUpRun.name !== dagData.dag.name))
      ? lookedUpRun
      : null;

  // Links from older versions and bookmarks redirect before a body mounts.
  const legacy = useLegacyRedirect(
    fileName,
    params.tab,
    dagData?.dag?.name ?? null
  );

  const dagLabels = [
    ...(dagData?.dag?.labels ?? []),
    ...(dagData?.dag?.tags ?? []),
  ];
  const dagWorkspaceName = workspaceNameFromLabels(dagLabels);
  const dagHasWorkspaceLabel = hasWorkspaceLabel(dagLabels);
  // The same page in the workspace of the workflow.
  const ownWorkspaceTo = useMemo(() => {
    const query = new URLSearchParams(location.search);
    query.set('workspace', dagWorkspaceName);
    return `${location.pathname}?${query.toString()}`;
  }, [dagWorkspaceName, location.pathname, location.search]);
  const dagMatchesWorkspace =
    workspaceSelection.kind === WorkspaceKind.all ||
    (workspaceSelection.kind === WorkspaceKind.default &&
      !dagHasWorkspaceLabel) ||
    (workspaceSelection.kind === WorkspaceKind.workspace &&
      dagWorkspaceName === workspaceSelection.workspace);

  // Use dagRunName from URL if available, otherwise use the name from dagData
  const dagRunName = queriedDAGRunName || dagData?.dag?.name || '';
  const effectiveDAGRunId = dagRunId;
  // The workspace selects its run itself; these reads serve the pop-out logs.
  const runReads = popout;
  const dagRunQueryEnabled = Boolean(
    runReads &&
      dagRunName &&
      effectiveDAGRunId &&
      !subDAGRunId &&
      dagMatchesWorkspace
  );

  // Fetch specific DAG-run data if dagRunId is provided
  const dagRunSSE = useDAGRunSSE(
    dagRunName,
    effectiveDAGRunId || '',
    dagRunQueryEnabled,
    remoteNode
  );
  const {
    data: dagRunResponse,
    error: dagRunError,
    mutate: mutateDagRun,
  } = useQuery(
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
    runReads && subDAGRunId && dagRunId && dagRunName && dagMatchesWorkspace
  );
  const subDAGRunSSE = useSubDAGRunSSE(
    dagRunName,
    dagRunId || '',
    subDAGRunId || '',
    subDAGRunQueryEnabled,
    remoteNode
  );
  const {
    data: subDAGRunResponse,
    error: subDAGRunError,
    mutate: mutateSubDagRun,
  } = useQuery(
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
  // A run that fails to load falls back to the latest run.
  const popoutRunLoading =
    !currentDAGRun &&
    (subDAGRunId
      ? subDAGRunQueryEnabled && !subDAGRunError
      : dagRunQueryEnabled && !dagRunError);

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

  // One value until one of its fields changes: the readers of the context do
  // not render again for each render of the page.
  const dagContext = useMemo(
    () => ({ refresh: refreshData, fileName, name: dagRunName }),
    [refreshData, fileName, dagRunName]
  );

  // The workflow waits for its redirects: a legacy link or the run file. A
  // lookup keeps the workspace that already shows the file: a run that it
  // selects is a run of its workflow, and a remount would reset the editor.
  const shownFile = useRef<string | null>(null);
  const redirecting =
    legacy === 'pending' ||
    (resolvingRunFile && shownFile.current !== fileName);
  const showBody =
    !!dagData?.dag && dagMatchesWorkspace && !redirecting && !ownRun;
  if (showBody) shownFile.current = fileName;
  const workspaceMismatch =
    !!dagData?.dag && !dagMatchesWorkspace && !redirecting && !ownRun;
  useFullBleed(showBody);
  // The workspace bar is the first row of the page.
  useHideBreadcrumb(showBody);
  // The bar and the loader stand in the place of the workspace until it shows.
  const loadingFrame =
    (!dagData?.dag && !dagError) || (!!dagData?.dag && redirecting);
  const legacyHistoryIdx =
    typeof legacy === 'object' ? legacy.needsHistory : null;

  return (
    <UnsavedChangesProvider>
      <RemoteNodeProvider remoteNode={remoteNode}>
        <DAGContext.Provider value={dagContext}>
          <div
            className={cn(
              'flex h-full min-h-0 w-full min-w-0 flex-col',
              // The frame is full bleed from the first paint: the messages
              // keep the padding of a page, the loading frame has none.
              !showBody && !loadingFrame && 'overflow-auto p-4 md:p-6'
            )}
          >
            {runFileRedirect && <Navigate replace to={runFileRedirect} />}
            {ownRun ? (
              <StandaloneRun
                run={ownRun}
                subRunId={searchParams.get('subRun')}
                remoteNode={remoteNode}
              />
            ) : (
              !dagData?.dag &&
              (dagError && !resolvingRunFile ? (
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
                <WorkspaceLoading fileName={fileName} />
              ))
            )}
            {dagData?.dag && redirecting && !ownRun && (
              <WorkspaceLoading fileName={fileName} />
            )}
            {showBody && popout && dagData && (
              <LogPopoutPage
                fileName={fileName}
                dagData={dagData}
                tab={params.tab}
                dagRun={currentDAGRun}
                runLoading={popoutRunLoading}
                refresh={refreshData}
              />
            )}
            {showBody && !popout && dagData && (
              <WorkflowWorkspace
                fileName={fileName}
                tab={legacyHistoryIdx === null ? params.tab : 'executions'}
                dagData={dagData}
                refreshDag={mutateDag}
                workspaceName={dagWorkspaceName || null}
                legacyHistoryIdx={legacyHistoryIdx}
              />
            )}
            {workspaceMismatch && (
              <div className="space-y-3 p-6 text-sm text-muted-foreground">
                <p>
                  <I18nText
                    text={'This workflow is not in the selected workspace.'}
                  />
                </p>
                <div className="flex flex-wrap gap-4">
                  <Link
                    className="underline hover:text-foreground"
                    to={buildUrl('/dags')}
                  >
                    {ts('Back to Workflows')}
                  </Link>
                  {queryWorkspace && (
                    <Link
                      className="underline hover:text-foreground"
                      to={allWorkspacesTo}
                      // Without ?workspace the selection of the app bar
                      // applies, and it can hide the workflow too.
                      onClick={() =>
                        appBarContext.selectWorkspace?.({
                          kind: WorkspaceKind.all,
                        })
                      }
                    >
                      {ts('Show in all workspaces')}
                    </Link>
                  )}
                  {dagWorkspaceName && (
                    <Link
                      className="underline hover:text-foreground"
                      to={ownWorkspaceTo}
                    >
                      {ts('Show in workspace {name}', {
                        name: dagWorkspaceName,
                      })}
                    </Link>
                  )}
                </div>
              </div>
            )}
          </div>
        </DAGContext.Provider>
      </RemoteNodeProvider>
    </UnsavedChangesProvider>
  );
}

export default DAGDetails;
