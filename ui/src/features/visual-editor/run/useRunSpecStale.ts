// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import {
  diffRunSpec,
  specTextDiffers,
  type RunSpecDiff,
  type RunSpecNames,
} from './runSpecDiff';

interface RunSpecs {
  /** The definition of the run; undefined until it is loaded. */
  run: string | undefined;
  /** The saved definition; undefined until it is loaded. */
  saved: string | undefined;
}

// The API has no file modification time, so the definitions are compared.
function useRunSpecs(p: {
  dagName: string;
  fileName: string;
  runId: string | null;
  /** Skips the request for the saved definition. */
  skipSaved?: boolean;
}): RunSpecs {
  const remoteNode = useRemoteNode();
  const runSpec = useQuery(
    '/dag-runs/{name}/{dagRunId}/spec',
    p.runId !== null && p.dagName
      ? {
          params: {
            path: { name: p.dagName, dagRunId: p.runId },
            query: { remoteNode },
          },
        }
      : null
  );
  const savedSpec = useQuery(
    '/dags/{fileName}/spec',
    p.runId !== null && p.fileName && !p.skipSaved
      ? { params: { path: { fileName: p.fileName }, query: { remoteNode } } }
      : null
  );
  return { run: runSpec.data?.spec, saved: savedSpec.data?.spec };
}

/**
 * True when the run used a definition different from the saved one. It is
 * false until both definitions are loaded. A null runId makes no request.
 */
export function useRunSpecStale(p: {
  dagName: string;
  fileName: string;
  runId: string | null;
}): boolean {
  const specs = useRunSpecs(p);
  return (
    p.runId !== null &&
    specs.run !== undefined &&
    specs.saved !== undefined &&
    specTextDiffers(specs.run, specs.saved)
  );
}

/**
 * The difference between the definition of the run and the live text (the
 * saved definition when `liveText` is undefined), as parsed values. Null until
 * both are loaded and for a null runId. It uses the requests of
 * `useRunSpecStale`, and the saved definition only when `liveText` is
 * undefined.
 */
export function useRunSpecDiff(p: {
  dagName: string;
  fileName: string;
  runId: string | null;
  liveText?: string;
  names?: RunSpecNames;
}): RunSpecDiff | null {
  const specs = useRunSpecs({ ...p, skipSaved: p.liveText !== undefined });
  const live = p.liveText ?? specs.saved;
  const { names } = p;
  return useMemo(
    () =>
      p.runId === null || specs.run === undefined || live === undefined
        ? null
        : diffRunSpec(specs.run, live, names),
    [p.runId, specs.run, live, names]
  );
}
