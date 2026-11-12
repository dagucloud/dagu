// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, ForeachItemStatusFilter } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { isSubDAGRun } from './useStepLogQuery';

type DAGRunDetails = components['schemas']['DAGRunDetails'];

/** The foreach step whose items are read, within its run. */
export type ForeachStepRef = {
  dagName: string;
  dagRunId: string;
  stepName: string;
  dagRun?: DAGRunDetails;
};

export const FOREACH_ITEMS_PER_PAGE = 50;

/** Polling cadence while the step or item is still running. */
const LIVE_REFRESH_MS = 2000;

// SWR skips a refresh that lands inside its dedupe window, so a window as
// long as the interval would halve the polling rate.
function liveOptions(live: boolean) {
  return {
    refreshInterval: live ? LIVE_REFRESH_MS : 0,
    dedupingInterval: LIVE_REFRESH_MS / 2,
    keepPreviousData: true,
    revalidateOnFocus: false,
  };
}

function routeOf(ref: ForeachStepRef) {
  const sub = isSubDAGRun(ref.dagRun);
  return {
    sub,
    path: {
      name: sub ? (ref.dagRun?.rootDAGRunName as string) : ref.dagName,
      dagRunId: sub ? (ref.dagRun?.rootDAGRunId as string) : ref.dagRunId,
      stepName: ref.stepName,
    },
    subDAGRunId: ref.dagRun?.dagRunId as string,
  };
}

/** Lists one page of a foreach step's items, polling while live. */
export function useForeachItems(
  ref: ForeachStepRef,
  query: { parent?: string; status?: ForeachItemStatusFilter; page: number },
  live: boolean
) {
  const remoteNode = useRemoteNode();
  const { sub, path, subDAGRunId } = routeOf(ref);
  const queryParams = {
    remoteNode,
    parent: query.parent,
    status: query.status,
    page: query.page,
    perPage: FOREACH_ITEMS_PER_PAGE,
  };
  const options = liveOptions(live);
  const rootQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/steps/{stepName}/foreach',
    whenEnabled(!sub, { params: { query: queryParams, path } }),
    options
  );
  const subQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/foreach',
    whenEnabled(sub, {
      params: { query: queryParams, path: { ...path, subDAGRunId } },
    }),
    options
  );
  return sub ? subQuery : rootQuery;
}

/** Reads one foreach item with its body steps, polling while live. */
export function useForeachItem(
  ref: ForeachStepRef,
  item: string,
  live: boolean
) {
  const remoteNode = useRemoteNode();
  const { sub, path, subDAGRunId } = routeOf(ref);
  const options = liveOptions(live);
  const rootQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/steps/{stepName}/foreach/{item}',
    whenEnabled(!sub, {
      params: { query: { remoteNode }, path: { ...path, item } },
    }),
    options
  );
  const subQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/foreach/{item}',
    whenEnabled(sub, {
      params: { query: { remoteNode }, path: { ...path, subDAGRunId, item } },
    }),
    options
  );
  return sub ? subQuery : rootQuery;
}
