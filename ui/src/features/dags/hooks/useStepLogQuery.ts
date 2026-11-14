// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, Stream } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';

type DAGRunDetails = components['schemas']['DAGRunDetails'];

/** Addresses a body step inside one item of a foreach step. */
export type ForeachLogTarget = {
  /** Name of the foreach step the item belongs to. */
  stepName: string;
  /** Item path as the foreach item routes address it. */
  item: string;
};

/** Which step log to read: a run's step, or a body step of a foreach item. */
export type StepLogSource = {
  dagName: string;
  dagRunId: string;
  /** The step whose log is read; a body step name when foreach is set. */
  stepName: string;
  /** Full run details, used to route a sub DAG-run through its root. */
  dagRun?: DAGRunDetails;
  foreach?: ForeachLogTarget;
};

type LogQuery = {
  stream: Stream;
  tail?: number;
  head?: number;
  offset?: number;
  limit?: number;
};

type SWROptions = {
  refreshInterval?: number;
  keepPreviousData?: boolean;
  revalidateOnFocus?: boolean;
  dedupingInterval?: number;
};

export function isSubDAGRun(dagRun?: DAGRunDetails): boolean {
  return (
    !!dagRun &&
    !!dagRun.rootDAGRunId &&
    !!dagRun.rootDAGRunName &&
    dagRun.rootDAGRunId !== dagRun.dagRunId
  );
}

/**
 * Reads a step log through whichever route addresses it: root or sub DAG-run,
 * plain step or foreach body step. All four routes return the same Log shape.
 */
export function useStepLogQuery(
  source: StepLogSource,
  query: LogQuery,
  swrOptions: SWROptions
) {
  const remoteNode = useRemoteNode();
  const { dagName, dagRunId, stepName, dagRun, foreach } = source;
  const sub = isSubDAGRun(dagRun);
  const rootPath = {
    name: sub ? (dagRun?.rootDAGRunName as string) : dagName,
    dagRunId: sub ? (dagRun?.rootDAGRunId as string) : dagRunId,
  };
  const subDAGRunId = dagRun?.dagRunId as string;
  const queryParams = { remoteNode, ...query };

  const stepQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/steps/{stepName}/log',
    whenEnabled(!sub && !foreach, {
      params: { query: queryParams, path: { ...rootPath, stepName } },
    }),
    swrOptions
  );
  const subStepQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/log',
    whenEnabled(sub && !foreach, {
      params: {
        query: queryParams,
        path: { ...rootPath, subDAGRunId, stepName },
      },
    }),
    swrOptions
  );
  const foreachQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/steps/{stepName}/foreach/{item}/steps/{bodyStepName}/log',
    whenEnabled(!sub && !!foreach, {
      params: {
        query: queryParams,
        path: {
          ...rootPath,
          stepName: foreach?.stepName as string,
          item: foreach?.item as string,
          bodyStepName: stepName,
        },
      },
    }),
    swrOptions
  );
  const subForeachQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/foreach/{item}/steps/{bodyStepName}/log',
    whenEnabled(sub && !!foreach, {
      params: {
        query: queryParams,
        path: {
          ...rootPath,
          subDAGRunId,
          stepName: foreach?.stepName as string,
          item: foreach?.item as string,
          bodyStepName: stepName,
        },
      },
    }),
    swrOptions
  );

  if (foreach) {
    return sub ? subForeachQuery : foreachQuery;
  }
  return sub ? subStepQuery : stepQuery;
}

/** Builds the download route for the same log the query reads. */
export function stepLogDownloadPath(
  apiURL: string,
  source: StepLogSource
): string {
  const { dagName, dagRunId, stepName, dagRun, foreach } = source;
  const segments = isSubDAGRun(dagRun)
    ? [
        'dag-runs',
        dagRun?.rootDAGRunName ?? '',
        dagRun?.rootDAGRunId ?? '',
        'sub-dag-runs',
        dagRun?.dagRunId ?? '',
      ]
    : ['dag-runs', dagName, dagRunId];
  if (foreach) {
    segments.push('steps', foreach.stepName, 'foreach', foreach.item);
  }
  segments.push('steps', stepName, 'log', 'download');
  return `${apiURL}/${segments.map(encodeURIComponent).join('/')}`;
}
