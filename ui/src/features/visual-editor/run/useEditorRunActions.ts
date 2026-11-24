// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import type { EditorRunActions, StepRunInputs } from '../contracts';
import { outputCache } from './outputCache';
import { buildDraftBody, buildStartBody } from './runRequests';

interface Args {
  fileName: string;
  dagName: string;
  spec: string;
  isDirty: boolean;
}

interface StartResult {
  data?: { dagRunId: string };
  error?: unknown;
  response: Response;
}

function serverMessage(error: unknown): string | null {
  if (typeof error !== 'object' || error === null) return null;
  const { message } = error as { message?: unknown };
  return typeof message === 'string' && message ? message : null;
}

// The steps of `fromRuns` whose run the server refused. The server names the
// first entry that it refuses: `step X cannot be reused from run Y: <reason>`.
function refusedSteps(
  message: string | null,
  fromRuns: Readonly<Record<string, string>>
): string[] {
  if (message === null) return [];
  return Object.entries(fromRuns)
    .filter(([step, runId]) =>
      message.includes(`step ${step} cannot be reused from run ${runId}:`)
    )
    .map(([step]) => step);
}

export function useEditorRunActions(args: Args): EditorRunActions {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const { ts } = useI18n();
  const [pending, setPending] = useState(0);
  const [error, setError] = useState<string | null>(null);

  // The returned functions stay stable and read the latest props.
  const latest = useRef({ ...args, remoteNode, ts });
  useLayoutEffect(() => {
    latest.current = { ...args, remoteNode, ts };
  });

  const start = useCallback(
    async (call: () => Promise<StartResult>): Promise<string | null> => {
      setError(null);
      setPending((n) => n + 1);
      const { ts } = latest.current;
      try {
        const { data, error, response } = await call();
        if (error !== undefined || !data) {
          setError(
            serverMessage(error) ??
              ts('Could not start the run (HTTP {status}).', {
                status: response.status,
              })
          );
          return null;
        }
        return data.dagRunId;
      } catch (e) {
        setError(
          e instanceof Error && e.message
            ? e.message
            : ts('Could not start the run.')
        );
        return null;
      } finally {
        setPending((n) => n - 1);
      }
    },
    []
  );

  const runDraft = useCallback(async () => {
    const { dagName, spec, remoteNode } = latest.current;
    return start(() =>
      client.POST('/dag-runs', {
        params: { query: { remoteNode } },
        body: buildDraftBody(spec, dagName),
      })
    );
  }, [client, start]);

  const runSteps = useCallback(
    async (steps: string[], opts?: StepRunInputs) => {
      const { fileName, dagName, spec, isDirty, remoteNode } = latest.current;
      const fromRuns = opts?.outputsFromRuns;
      const step = {
        steps,
        outputsFromRunId: opts?.reuseOutputsFrom,
        ...(fromRuns ? { outputsFromRuns: fromRuns } : {}),
        ...(opts?.reused ? { reused: opts.reused } : {}),
      };
      // A cache entry whose run the server refuses is dropped, so that the
      // next run of the step plans that step to run instead.
      const send = (call: () => Promise<StartResult>) =>
        start(async () => {
          const result = await call();
          if (fromRuns && result.error !== undefined) {
            const scope = { remoteNode, fileName };
            const refused = refusedSteps(serverMessage(result.error), fromRuns);
            for (const name of refused) {
              if (outputCache.entries(scope)[name]?.runId === fromRuns[name]) {
                outputCache.remove(scope, name);
              }
            }
          }
          return result;
        });
      // The steps of an unsaved buffer run from the buffer, not the saved
      // file. They are a test of a draft: the run leaves no trace.
      if (isDirty) {
        return send(() =>
          client.POST('/dag-runs', {
            params: { query: { remoteNode } },
            body: buildDraftBody(spec, dagName, { ...step, ephemeral: true }),
          })
        );
      }
      return send(() =>
        client.POST('/dags/{fileName}/start', {
          params: { path: { fileName }, query: { remoteNode } },
          body: buildStartBody(step),
        })
      );
    },
    [client, start]
  );

  const runStep = useCallback(
    (stepName: string, opts?: StepRunInputs) => runSteps([stepName], opts),
    [runSteps]
  );

  return {
    runDraft,
    runStep,
    runSteps,
    isStarting: pending > 0,
    error,
  };
}
