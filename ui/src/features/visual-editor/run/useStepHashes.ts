// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import type { ApiDAGDetails } from '../contracts';
import { stepHashes } from './stepHashes';

/**
 * The step hashes of the definition that the editor holds now: the live text,
 * or the saved definition when `liveText` is undefined. Null until the text
 * is known, and while `enabled` is false (no request is made then).
 */
export function useLiveStepHashes(p: {
  fileName: string;
  liveText?: string;
  dag?: ApiDAGDetails;
  enabled: boolean;
}): ReadonlyMap<string, string> | null {
  const remoteNode = useRemoteNode();
  const saved = useQuery(
    '/dags/{fileName}/spec',
    p.enabled && p.liveText === undefined && p.fileName
      ? { params: { path: { fileName: p.fileName }, query: { remoteNode } } }
      : null
  );
  const text = p.enabled ? (p.liveText ?? saved.data?.spec) : undefined;
  const { dag } = p;
  // A definition that cannot be read gives no hash: nothing counts as fresh.
  const failed = saved.error !== undefined && saved.error !== null;
  return useMemo(
    () =>
      text === undefined
        ? failed
          ? new Map<string, string>()
          : null
        : stepHashes(text, dag),
    [text, dag, failed]
  );
}

/**
 * The step hashes of the definition that a run used. Null until the run
 * definition is loaded, and while `enabled` is false (no request is made).
 */
export function useRunStepHashes(p: {
  dagName: string;
  runId: string | null;
  names?: ApiDAGDetails;
  enabled: boolean;
}): ReadonlyMap<string, string> | null {
  const remoteNode = useRemoteNode();
  const spec = useQuery(
    '/dag-runs/{name}/{dagRunId}/spec',
    p.enabled && p.runId !== null && p.dagName
      ? {
          params: {
            path: { name: p.dagName, dagRunId: p.runId },
            query: { remoteNode },
          },
        }
      : null
  );
  const text = spec.data?.spec;
  const { names } = p;
  return useMemo(
    () => (text === undefined ? null : stepHashes(text, names)),
    [text, names]
  );
}
