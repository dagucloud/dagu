// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { getQueryErrorMessage, isNotFoundQueryError } from '@/lib/queryErrors';
import type { RunRef } from '../data/types';

export interface RunSpec {
  /** The YAML to show: the snapshot of the run, or the saved one when it is missing. */
  text: string | null;
  /** The run has no stored definition (404 or empty), so text is the saved definition. */
  missing: boolean;
  isLoading: boolean;
  /**
   * Not null when a read failed, other than a 404 of the snapshot: the message
   * of the error, or '' when it has none.
   */
  error: string | null;
}

/**
 * The definition that a run used. A null ref makes no request. The saved
 * definition uses the SWR key and params of DAGSpec, so the two share one
 * request.
 */
export function useRunSpec(ref: RunRef | null, fileName: string): RunSpec {
  const remoteNode = useRemoteNode();
  const runQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/spec',
    whenEnabled(ref !== null, {
      params: {
        path: { name: ref?.name ?? '', dagRunId: ref?.runId ?? '' },
        query: { remoteNode },
      },
    })
  );
  const snapshot = runQuery.data?.spec;
  const missing =
    ref !== null &&
    ((runQuery.error != null && isNotFoundQueryError(runQuery.error)) ||
      (runQuery.data !== undefined && !snapshot));

  const savedQuery = useQuery(
    '/dags/{fileName}/spec',
    whenEnabled(missing, {
      params: {
        query: { remoteNode },
        path: { fileName },
      },
    })
  );

  if (ref === null) {
    return { text: null, missing: false, isLoading: false, error: null };
  }
  if (missing) {
    return {
      text: savedQuery.data?.spec ?? null,
      missing: true,
      isLoading: savedQuery.data === undefined && savedQuery.error == null,
      error: errorMessage(savedQuery.error),
    };
  }
  return {
    text: snapshot ?? null,
    missing: false,
    isLoading: runQuery.data === undefined && runQuery.error == null,
    error: errorMessage(runQuery.error),
  };
}

function errorMessage(error: unknown): string | null {
  return error == null ? null : getQueryErrorMessage(error, '');
}
