// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import type { EditorRunActions, StepRunInputs } from '../contracts';
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

  const runDag = useCallback(async () => {
    const { fileName, isDirty, remoteNode, ts } = latest.current;
    if (isDirty) {
      setError(ts('Save the DAG first, or run the unsaved draft.'));
      return null;
    }
    return start(() =>
      client.POST('/dags/{fileName}/start', {
        params: { path: { fileName }, query: { remoteNode } },
        body: buildStartBody(),
      })
    );
  }, [client, start]);

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
      const step = {
        steps,
        outputsFromRunId: opts?.reuseOutputsFrom,
        ...(opts?.outputsFromRuns
          ? { outputsFromRuns: opts.outputsFromRuns }
          : {}),
        ...(opts?.reused ? { reused: opts.reused } : {}),
      };
      // The steps of an unsaved buffer run from the buffer, not the saved
      // file. They are a test of a draft: the run leaves no trace.
      if (isDirty) {
        return start(() =>
          client.POST('/dag-runs', {
            params: { query: { remoteNode } },
            body: buildDraftBody(spec, dagName, { ...step, ephemeral: true }),
          })
        );
      }
      return start(() =>
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
    runDag,
    runDraft,
    runStep,
    runSteps,
    isStarting: pending > 0,
    error,
  };
}
