// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useQuery } from '@/hooks/api';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';

/** Persisted outputs of one step in a run. No request when an argument is null or empty. */
export function useStepOutputs(
  dagName: string,
  runId: string | null,
  stepName: string | null
) {
  const remoteNode = useRemoteNode();
  return useQuery(
    '/dag-runs/{name}/{dagRunId}/steps/{stepName}/outputs',
    dagName && runId && stepName
      ? {
          params: {
            path: { name: dagName, dagRunId: runId, stepName },
            query: { remoteNode },
          },
        }
      : null
  );
}
