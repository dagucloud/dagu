// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import type { components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import type { EditorRunActions, StepRunInputs } from '../contracts';
import { buildDraftBody, buildStartBody } from './runRequests';

type CarriedStepCheck = components['schemas']['CarriedStepCheck'];

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

// The carried steps that a refused start lists in details.refusedSteps,
// or null when the error is of another kind.
function refusedSteps(error: unknown): CarriedStepCheck[] | null {
  if (typeof error !== 'object' || error === null) return null;
  const { details } = error as { details?: { refusedSteps?: unknown } };
  const steps = details?.refusedSteps;
  return Array.isArray(steps) && steps.length > 0
    ? (steps as CarriedStepCheck[])
    : null;
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
    async (
      call: () => Promise<StartResult>,
      onRefused?: StepRunInputs['onRefused']
    ): Promise<string | null> => {
      setError(null);
      setPending((n) => n + 1);
      const { ts } = latest.current;
      try {
        const { data, error, response } = await call();
        const refused = onRefused ? refusedSteps(error) : null;
        if (refused) {
          onRefused?.(refused);
          return null;
        }
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
    const { fileName, dagName, spec, remoteNode } = latest.current;
    return start(() =>
      client.POST('/dag-runs', {
        params: { query: { remoteNode } },
        body: buildDraftBody(spec, dagName, fileName),
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
        ...(opts?.allowChangedSteps
          ? { allowChangedSteps: opts.allowChangedSteps }
          : {}),
        ...(opts?.profile !== undefined ? { profile: opts.profile } : {}),
      };
      // The steps of an unsaved buffer run from the buffer, not the saved
      // file. They are a test of a draft: the run leaves no trace.
      if (isDirty) {
        return start(
          () =>
            client.POST('/dag-runs', {
              params: { query: { remoteNode } },
              body: buildDraftBody(spec, dagName, fileName, {
                ...step,
                ephemeral: true,
              }),
            }),
          opts?.onRefused
        );
      }
      return start(
        () =>
          client.POST('/dags/{fileName}/start', {
            params: { path: { fileName }, query: { remoteNode } },
            body: buildStartBody(step),
          }),
        opts?.onRefused
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
