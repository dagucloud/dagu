// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useState } from 'react';
import type { components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';

type CarriedStepCheck = components['schemas']['CarriedStepCheck'];
type StepReuseCheck = components['schemas']['StepReuseCheck'];

/** What the server answered for the previous steps of a step run. */
export interface ReuseCheck {
  /** 'checking' until every request answered, 'failed' when one failed. */
  state: 'checking' | 'ready' | 'failed';
  /** The verdict of each step that the source run gives, by step name. */
  fromSource: Readonly<Record<string, CarriedStepCheck>>;
  /** The verdict of the cache entry of each step, by step name. */
  fromCache: Readonly<Record<string, CarriedStepCheck>>;
  /** Why the check failed; null unless the state is 'failed'. */
  error: string | null;
  /** Asks the server again. */
  retry(): void;
}

type Answer = Pick<ReuseCheck, 'state' | 'fromSource' | 'fromCache' | 'error'>;

const NONE: Readonly<Record<string, CarriedStepCheck>> = {};
const CHECKING: Answer = {
  state: 'checking',
  fromSource: NONE,
  fromCache: NONE,
  error: null,
};

// The verdicts of an answer by step, for the run that the request named.
function byStep(
  check: StepReuseCheck | null
): Record<string, CarriedStepCheck> {
  return Object.fromEntries(
    (check?.carried ?? []).map((carried) => [carried.step, carried])
  );
}

function errorMessage(error: unknown, status: number): string {
  if (typeof error === 'object' && error !== null) {
    const { message } = error as { message?: unknown };
    if (typeof message === 'string' && message) return message;
  }
  return `HTTP ${status}`;
}

/**
 * Asks the server what changed for each previous step of a step run since
 * the run that would give its outputs, as the start would check it: one
 * request for the source run and one for the cache entries, planned with the
 * same code as the start. It belongs to the step run dialog only: it asks
 * once each time the dialog, the source, the entries or the text change, and
 * never polls. A request that is no longer current is aborted.
 */
export function useReuseCheck(p: {
  fileName: string;
  dagName: string;
  step: string;
  /** The run whose outputs the skipped steps carry, or null. */
  sourceRunId: string | null;
  /** The run of the cache entry of each previous step, by step name. */
  entries: Readonly<Record<string, string>>;
  /** The unsaved editor text the step runs from; null for the saved file. */
  draft: string | null;
  /** False while the source and the entries are still being chosen. */
  enabled: boolean;
}): ReuseCheck {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const [attempt, setAttempt] = useState(0);
  const entriesKey = JSON.stringify(
    Object.entries(p.entries).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
  );
  const key = JSON.stringify([
    remoteNode,
    p.fileName,
    p.dagName,
    p.step,
    p.sourceRunId,
    entriesKey,
    p.draft,
    attempt,
  ]);
  const [answer, setAnswer] = useState<{ key: string; value: Answer } | null>(
    null
  );
  const { fileName, dagName, step, sourceRunId, draft, enabled } = p;

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    const entries = Object.fromEntries(
      JSON.parse(entriesKey) as [string, string][]
    );
    const preview = (selection: {
      outputsFromRunId?: string;
      outputsFromRuns?: Record<string, string>;
    }) =>
      draft === null
        ? client.POST('/dags/{fileName}/start-preview', {
            params: { path: { fileName }, query: { remoteNode } },
            body: { steps: [step], ...selection },
            signal: controller.signal,
          })
        : client.POST('/dag-runs/preview', {
            params: { query: { remoteNode } },
            body: {
              spec: draft,
              name: dagName,
              draftOf: fileName,
              steps: [step],
              ...selection,
            },
            signal: controller.signal,
          });
    const ask = async (selection: Parameters<typeof preview>[0] | null) => {
      if (selection === null) return null;
      const { data, error, response } = await preview(selection);
      if (error !== undefined || !data) {
        throw new Error(errorMessage(error, response.status));
      }
      return data;
    };
    Promise.all([
      ask(sourceRunId !== null ? { outputsFromRunId: sourceRunId } : null),
      ask(
        Object.keys(entries).length > 0 ? { outputsFromRuns: entries } : null
      ),
    ])
      .then(([source, cache]) => {
        if (controller.signal.aborted) return;
        setAnswer({
          key,
          value: {
            state: 'ready',
            fromSource: byStep(source),
            fromCache: byStep(cache),
            error: null,
          },
        });
      })
      .catch((e: unknown) => {
        if (controller.signal.aborted) return;
        setAnswer({
          key,
          value: {
            ...CHECKING,
            state: 'failed',
            error: e instanceof Error && e.message ? e.message : String(e),
          },
        });
      });
    return () => controller.abort();
  }, [
    client,
    dagName,
    draft,
    enabled,
    entriesKey,
    fileName,
    key,
    remoteNode,
    sourceRunId,
    step,
  ]);

  const retry = useCallback(() => setAttempt((n) => n + 1), []);
  const value = answer?.key === key ? answer.value : CHECKING;
  return { ...value, retry };
}
