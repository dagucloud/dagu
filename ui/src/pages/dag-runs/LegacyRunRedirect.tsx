// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { Navigate, useLocation, useParams } from 'react-router-dom';
import { WorkspaceLoading } from '@/features/workflow-workspace/WorkspaceLoading';
import { AppBarContext } from '../../contexts/AppBarContext';
import { legacyRunPageRedirectURL } from '../../features/dag-runs/lib/dagRunUrls';
import { useQuery } from '../../hooks/api';
import { whenEnabled } from '../../hooks/queryUtils';

/**
 * The route /dag-runs/:name/:dagRunId. The run page is the workflow page
 * with ?run now, so an old link or bookmark redirects there. The latest run
 * goes there by its ID, which finds the file of a DAG named apart from it.
 */
export default function LegacyRunRedirect(): React.ReactElement {
  const { name = '', dagRunId = 'latest' } = useParams();
  const location = useLocation();
  const appBarRemoteNode = React.useContext(AppBarContext).selectedRemoteNode;
  const fallbackRemoteNode = appBarRemoteNode?.trim() || 'local';
  const search = new URLSearchParams(location.search);
  const latest = dagRunId === 'latest' && !search.get('subDAGRunId');
  const { data, error } = useQuery(
    '/dag-runs/{name}/{dagRunId}',
    whenEnabled(latest, {
      params: {
        path: { name, dagRunId },
        query: {
          remoteNode: search.get('remoteNode')?.trim() || fallbackRemoteNode,
        },
      },
    })
  );
  if (latest && !data && !error) return <WorkspaceLoading fileName={name} />;
  const to = legacyRunPageRedirectURL(
    name,
    data?.dagRunDetails?.dagRunId ?? dagRunId,
    search,
    fallbackRemoteNode
  );
  return <Navigate to={to} replace />;
}
