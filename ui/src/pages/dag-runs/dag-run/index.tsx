// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useCallback, useContext, useEffect } from 'react';
import { useParams } from 'react-router-dom';
import { AppBarContext } from '../../../contexts/AppBarContext';
import { usePageContext } from '../../../contexts/PageContext';
import { RemoteNodeProvider } from '../../../contexts/RemoteNodeContext';
import { DAGRunDetailsContent } from '../../../features/dag-runs/components/dag-run-details';
import { DAGRunContext } from '../../../features/dag-runs/contexts/DAGRunContext';
import { matchesRequestedDAGRunDetails } from '../../../features/dag-runs/hooks/dagRunDetailsRequest';
import { useBoundedDAGRunDetails } from '../../../features/dag-runs/hooks/useBoundedDAGRunDetails';

type ApiError = {
  response?: { status?: number };
  message?: string;
};

type ErrorDisplayProps = {
  error: unknown;
  name: string | undefined;
  dagRunId: string | undefined;
};

function ErrorDisplay({ error, name, dagRunId }: ErrorDisplayProps) {
  const apiError = error as ApiError;
  const statusCode = apiError?.response?.status;
  const isNotFound = statusCode === 404;

  const containerClass = isNotFound ? 'bg-muted' : 'bg-error-muted';
  const titleClass = isNotFound ? 'text-foreground' : 'text-error';
  const messageClass = isNotFound ? 'text-muted-foreground' : 'text-error';

  const title = isNotFound ? 'DAG Run Not Found' : 'Error Loading DAG Run';
  const message = isNotFound
    ? 'This DAG run may have been dequeued or removed. The previous state is no longer available.'
    : apiError?.message || 'Failed to load DAG run details';

  return (
    <div className="w-full px-4">
      <div className={`${containerClass} rounded-lg p-6 m-4`}>
        <h2 className={`text-lg font-semibold ${titleClass} mb-2`}>{title}</h2>
        <p className={messageClass}>{message}</p>
        {isNotFound && (
          <p className="text-sm text-muted-foreground mt-2">
            DAG: {name} | Run ID: {dagRunId}
          </p>
        )}
      </div>
    </div>
  );
}

function DAGRunDetailsPage() {
  const { name, dagRunId = 'latest' } = useParams();
  const appBarContext = useContext(AppBarContext);
  const { setContext } = usePageContext();

  const searchParams = new URLSearchParams(window.location.search);
  const subDAGRunId = searchParams.get('subDAGRunId');
  const parentDAGRunId = searchParams.get('dagRunId');
  const parentName = searchParams.get('dagRunName') || name;

  const canQuerySubDag = !!(subDAGRunId && parentDAGRunId && parentName);
  const queryRemoteNode = searchParams.get('remoteNode')?.trim();
  const appBarRemoteNode = appBarContext.selectedRemoteNode?.trim();
  const remoteNode = queryRemoteNode || appBarRemoteNode || 'local';
  const detailsTarget = canQuerySubDag
    ? {
        remoteNode,
        name: name || '',
        dagRunId: dagRunId || 'latest',
        parentName: parentName as string,
        parentDAGRunId: parentDAGRunId as string,
        subDAGRunId: subDAGRunId as string,
      }
    : name
      ? {
          remoteNode,
          name,
          dagRunId: dagRunId || 'latest',
        }
      : null;

  const {
    data: latestDetails,
    error,
    refresh,
  } = useBoundedDAGRunDetails({
    target: detailsTarget,
    enabled: detailsTarget !== null,
    pollIntervalMs: detailsTarget ? 2000 : 0,
  });

  const refreshFn = useCallback(() => {
    setTimeout(() => {
      void refresh();
    }, 500);
  }, [refresh]);

  const expectedDagRunId = subDAGRunId || dagRunId || 'latest';
  const dagRunDetails = matchesRequestedDAGRunDetails(
    latestDetails,
    expectedDagRunId,
    subDAGRunId ? undefined : name
  )
    ? latestDetails
    : null;
  const displayDAGRunId = subDAGRunId || dagRunId || '';

  function getDisplayName(): string {
    if (subDAGRunId) {
      return dagRunDetails?.name || parentName || '';
    }
    return name || '';
  }
  const displayName = getDisplayName();

  useEffect(() => {
    if (!displayName) {
      return;
    }
    setContext({
      dagFile: displayName,
      dagRunId: displayDAGRunId || undefined,
      dagRunName: displayName,
      source: 'dag-run-details-page',
    });
    return () => setContext(null);
  }, [displayName, displayDAGRunId, setContext]);

  if (error && !dagRunDetails) {
    return <ErrorDisplay error={error} name={name} dagRunId={dagRunId} />;
  }

  if (!dagRunDetails) {
    return null;
  }

  return (
    <div className="flex h-full min-h-0 w-full max-w-7xl flex-col px-4">
      <RemoteNodeProvider remoteNode={remoteNode}>
        <DAGRunContext.Provider
          value={{
            refresh: refreshFn,
            name: displayName,
            dagRunId: displayDAGRunId || '',
          }}
        >
          <DAGRunDetailsContent
            name={displayName}
            dagRun={dagRunDetails}
            refreshFn={refreshFn}
            dagRunId={displayDAGRunId}
            fillHeight
          />
        </DAGRunContext.Provider>
      </RemoteNodeProvider>
    </div>
  );
}

export default DAGRunDetailsPage;
