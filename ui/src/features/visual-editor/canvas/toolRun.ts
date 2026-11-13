// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext, useContext, useMemo } from 'react';
import type { NodeStatus, Status, components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import type { ToolSubRuns } from '../run/toolCalls';
import { subRunLink, type PageLink } from '../tools/targets';

type ToolDefinition = components['schemas']['ToolDefinition'];

/**
 * The run that the canvas shows, for the tool satellites of its chat steps:
 * the run that holds their effective catalog, and how many times each tool
 * was called, by server step name and tool DAG name.
 */
export interface ToolRunInfo {
  dagName: string;
  runId: string | null;
  calls: Record<string, Record<string, number>>;
  /** The sub-run of each call, by server step name and tool DAG name: where the entry of a DAG leads. */
  subRuns?: ToolSubRuns;
  /** The root run that holds the shown run: the page of a sub-run opens on it. */
  root?: { name: string; id: string; fileName?: string };
}

export const ToolRunContext = createContext<ToolRunInfo | null>(null);

export function useToolRun(): ToolRunInfo | null {
  return useContext(ToolRunContext);
}

/**
 * The effective catalog of a chat step in the shown run, or undefined without
 * a run or while it loads. While the step runs the catalog is read again, as
 * the step builds it when it starts. Cards of one step share one request.
 */
export function useToolDefinitions(
  stepName: string | undefined,
  active: boolean
): ToolDefinition[] | undefined {
  const run = useToolRun();
  const remoteNode = useRemoteNode();
  const enabled = Boolean(run?.runId && stepName);
  const { data } = useQuery(
    '/dag-runs/{name}/{dagRunId}/steps/{stepName}/messages',
    whenEnabled(enabled, {
      params: {
        path: {
          name: run?.dagName ?? '',
          dagRunId: run?.runId ?? '',
          stepName: stepName ?? '',
        },
        query: { remoteNode },
      },
    }),
    { refreshInterval: active ? 3000 : 0, revalidateOnFocus: false }
  );
  return enabled ? data?.toolDefinitions : undefined;
}

/** The latest call of a tool DAG in the shown run, with what the server knows of its child run. */
export interface DagToolRun {
  /** How many sub-runs the step made of this DAG. */
  count: number;
  /** The id of the latest sub-run. */
  subRunId: string;
  /** The status of the latest sub-run, undefined while it loads. */
  status: Status | undefined;
  /** The status of each step of the latest sub-run, by step name. */
  steps: Record<string, NodeStatus> | undefined;
  /** The latest sub-run in full, for the state of its steps in the enlarged view. */
  details: components['schemas']['DAGRunDetails'] | undefined;
  /** The page of the latest sub-run. */
  link: PageLink | null;
}

/**
 * The latest sub-run of the tool DAG `dagName` of the step `stepName` in the
 * shown run, or null when the run has none (no run, or the model never called
 * the tool). The status comes from one request for the sub-run, only while
 * `enabled`; it is read again every few seconds while the step `active`ly runs.
 */
export function useDagToolRun(
  stepName: string | undefined,
  dagName: string,
  enabled: boolean,
  active: boolean
): DagToolRun | null {
  const run = useToolRun();
  const remoteNode = useRemoteNode();
  const list =
    stepName === undefined ? undefined : run?.subRuns?.[stepName]?.[dagName];
  const last = list && list.length > 0 ? list[list.length - 1] : undefined;
  const root = run?.root;
  const fetch = enabled && !!last && !!root;
  const { data } = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}',
    whenEnabled(fetch, {
      params: {
        path: {
          name: root?.name ?? '',
          dagRunId: root?.id ?? '',
          subDAGRunId: last?.dagRunId ?? '',
        },
        query: { remoteNode },
      },
    }),
    { refreshInterval: active ? 3000 : 0, revalidateOnFocus: false }
  );
  return useMemo(() => {
    if (!list || !last) return null;
    const details = fetch ? data?.dagRunDetails : undefined;
    const steps: Record<string, NodeStatus> = {};
    for (const node of details?.nodes ?? []) {
      steps[node.step.name] = node.status;
    }
    return {
      count: list.length,
      subRunId: last.dagRunId,
      status: details?.status,
      steps: details ? steps : undefined,
      details,
      link: root ? subRunLink(root, last.dagRunId, remoteNode) : null,
    };
  }, [list, last, fetch, data?.dagRunDetails, root, remoteNode]);
}
