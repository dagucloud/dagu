// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useMemo } from 'react';
import { NodeStatus } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import type { ApiDAGDetails, StepOutputs } from '../contracts';
import {
  isCacheable,
  outputCache,
  sameEntry,
  type CacheEntry,
  type CacheScope,
} from './outputCache';
import { paramsDiffer } from './reuseSource';
import { useCacheScope, useOutputCacheEntries } from './useOutputCacheEntries';
import { useLiveStepHashes, useRunStepHashes } from './useStepHashes';

/**
 * The values of a step that succeeded, by output name, or null when there is
 * nothing to keep: the step did not succeed, it has no output, or a value was
 * cut by the server (a cut value is not the value).
 */
export function cacheableValues(
  data: StepOutputs | undefined
): Record<string, string> | null {
  if (!data || data.status !== NodeStatus.Success) return null;
  const all = [
    ...data.outputs,
    ...(data.outputVariable ? [data.outputVariable] : []),
  ];
  if (all.some((output) => output.truncated)) return null;
  const values = Object.fromEntries(all.map((o) => [o.name, o.value]));
  return isCacheable(values) ? values : null;
}

// The runs and values that a capture already stored, by workflow and step.
// A pair is stored once, so that two views of different runs do not replace
// each other's entry again and again. A retry keeps its run id: its new
// values are another pair.
const captured = new Map<string, Set<string>>();

function capturedKey(scope: CacheScope, step: string): string {
  return `${scope.remoteNode}\u0000${scope.fileName}\u0000${step}`;
}

/**
 * Lets the next read of a step capture again, after its entry was removed
 * from the cache (the page never removes one; the tests do).
 */
export function forgetCaptures(scope: CacheScope, step: string): void {
  captured.delete(capturedKey(scope, step));
}

/**
 * Keeps the outputs of a step that succeeded in the cache of the workflow,
 * with the hash of the step as the run saw it. The caller passes the data
 * that it already loaded (`useStepOutputs`): this hook makes no request for
 * outputs. It reads the definition and the parameters of the run, and the
 * defaults of the workflow, once. Nothing happens without a file name. Every
 * entry names the run that it was read from. An entry that is fresh for the
 * saved definition is not replaced by the step of an older run that the user
 * looks at. A run with other parameters than the defaults of the workflow is
 * not kept: a step run uses the defaults, and the hash does not cover the
 * parameters of a run.
 */
export function useCaptureStepOutputs(p: {
  /** The workflow file; no capture without it. */
  fileName: string | undefined;
  dagName: string;
  runId: string | null;
  /** Server name of the step. */
  step: string;
  data: StepOutputs | undefined;
}): void {
  const { fileName, dagName, runId, step, data } = p;
  const scope = useCacheScope(fileName ?? '');
  const entries = useOutputCacheEntries(scope);
  const values = fileName ? cacheableValues(data) : null;
  const { remoteNode } = scope;
  const run = useQuery(
    '/dag-runs/{name}/{dagRunId}',
    values !== null && fileName && dagName && runId
      ? {
          params: {
            path: { name: dagName, dagRunId: runId },
            query: { remoteNode },
          },
        }
      : null
  );
  const workflow = useQuery(
    '/dags/{fileName}',
    values !== null && fileName
      ? { params: { path: { fileName }, query: { remoteNode } } }
      : null
  );
  // The server names a step that has no name: the run gives the names of its
  // steps, in order.
  const runNodes = run.data?.dagRunDetails.nodes;
  const runNames = useMemo<ApiDAGDetails | undefined>(
    () =>
      runNodes
        ? { name: dagName, steps: runNodes.map((node) => node.step) }
        : undefined,
    [dagName, runNodes]
  );
  const hashes = useRunStepHashes({
    dagName,
    runId,
    ...(runNames ? { names: runNames } : {}),
    enabled: values !== null,
  });
  // The saved definition: an entry that is fresh for it is kept when the user
  // looks at an older run, whose step is not the step of the file any more.
  const savedDag = workflow.data?.dag;
  const saved = useLiveStepHashes({
    fileName: fileName ?? '',
    ...(savedDag ? { dag: savedDag } : {}),
    enabled: values !== null && !!fileName,
  });
  // Null until the run and the workflow answer.
  const ranWithDefaults =
    run.data && workflow.data
      ? !paramsDiffer(
          run.data.dagRunDetails.params,
          workflow.data.dag?.defaultParams
        )
      : null;
  const held = entries[step];
  const hash = hashes?.get(step);
  const savedHash = saved?.get(step);
  useEffect(() => {
    if (!fileName || !runId || values === null || hash === undefined) return;
    // Wait for the saved definition, so that a stale read never replaces a
    // fresh entry for a moment.
    if (saved === null) return;
    if (ranWithDefaults !== true) return;
    if (
      held !== undefined &&
      savedHash !== undefined &&
      held.stepHash === savedHash &&
      hash !== savedHash
    ) {
      return;
    }
    const entry: CacheEntry = {
      values,
      runId,
      capturedAt: Date.now(),
      stepHash: hash,
    };
    const seen = captured.get(capturedKey(scope, step)) ?? new Set<string>();
    const pair = `${runId}\u0000${JSON.stringify(values)}`;
    if (seen.has(pair) || sameEntry(held, entry)) return;
    if (outputCache.put(scope, step, entry)) {
      captured.set(capturedKey(scope, step), seen.add(pair));
    }
    // `values` is rebuilt for each render: its content is the dependency.
  }, [
    fileName,
    scope,
    runId,
    step,
    hash,
    savedHash,
    saved === null,
    ranWithDefaults,
    JSON.stringify(values),
    held,
  ]);
}
