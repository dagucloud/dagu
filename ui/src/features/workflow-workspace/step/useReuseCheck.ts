// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useSyncExternalStore } from 'react';
import type { components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';

type CarriedStepCheck = components['schemas']['CarriedStepCheck'];
type StepReuseCheck = components['schemas']['StepReuseCheck'];
type Client = ReturnType<typeof useClient>;

/**
 * What the server answered for the previous steps of a step run. 'checking'
 * until it answers; 'failed' when the check failed; 'unsupported' when the
 * server has no check (an older remote node).
 */
export interface ReuseCheck {
  state: 'checking' | 'ready' | 'failed' | 'unsupported';
  /** The verdict of each step that the source run gives, by step name. */
  fromSource: Readonly<Record<string, CarriedStepCheck>>;
  /** The verdict of the cache entry of each step, by step name. */
  fromCache: Readonly<Record<string, CarriedStepCheck>>;
  /** The runtime profile the run would use ('' for none); null until ready. */
  profile: string | null;
  /** Why the check failed; null unless the state is 'failed'. */
  error: string | null;
  /** Asks the server again, past an answer that is kept. */
  retry(): void;
}

/** What a step run would carry, as the server checks it. */
export interface ReuseCheckInput {
  fileName: string;
  dagName: string;
  step: string;
  /** The run whose outputs the skipped steps carry, or null. */
  sourceRunId: string | null;
  /** The run of the cache entry of each previous step, by step name. */
  entries: Readonly<Record<string, string>>;
  /** The unsaved editor text the step runs from; null for the saved file. */
  draft: string | null;
  /** The runtime profile the user chose; undefined for the default one. */
  profile?: string;
}

type Answer = Omit<ReuseCheck, 'retry'>;

const NONE: Readonly<Record<string, CarriedStepCheck>> = {};
const CHECKING: Answer = {
  state: 'checking',
  fromSource: NONE,
  fromCache: NONE,
  profile: null,
  error: null,
};

/** How long an answer stands for the same step run without a new check. */
export const VERDICT_FRESH_MS = 60_000;
/** The most answers that the page keeps. */
const MAX_VERDICTS = 32;

interface Stored {
  at: number;
  answer: Answer;
}

// The answers are kept for the page, by the step run they are for, so that
// the dialog and the INPUT column of the step show the same one, and opening
// the dialog again does not ask again.
const verdicts = new Map<string, Stored>();
const pending = new Set<string>();
const listeners = new Set<() => void>();

function publish(key: string, answer: Answer): void {
  verdicts.delete(key);
  verdicts.set(key, { at: Date.now(), answer });
  for (const oldest of verdicts.keys()) {
    if (verdicts.size <= MAX_VERDICTS) break;
    verdicts.delete(oldest);
  }
  listeners.forEach((listener) => listener());
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** Forgets every answer: the tests start from none. */
export function clearReuseVerdicts(): void {
  verdicts.clear();
  pending.clear();
  listeners.forEach((listener) => listener());
}

// An answer stands for VERDICT_FRESH_MS, and not when it is older than since.
function fresh(stored: Stored | undefined, since = 0): stored is Stored {
  return (
    stored !== undefined &&
    stored.answer.state !== 'checking' &&
    stored.at >= since &&
    Date.now() - stored.at < VERDICT_FRESH_MS
  );
}

// The verdicts of an answer by step.
function byStep(
  check: StepReuseCheck | null
): Record<string, CarriedStepCheck> {
  return Object.fromEntries(
    (check?.carried ?? []).map((carried) => [carried.step, carried])
  );
}

// An API error has a code; an older server answers an unknown path with text.
function isApiError(error: unknown): boolean {
  return (
    typeof error === 'object' &&
    error !== null &&
    typeof (error as { code?: unknown }).code === 'string'
  );
}

function errorMessage(error: unknown, status: number): string {
  if (typeof error === 'object' && error !== null) {
    const { message } = error as { message?: unknown };
    if (typeof message === 'string' && message) return message;
  }
  return `HTTP ${status}`;
}

/** The server has no check of carried steps: an older remote node. */
class Unsupported extends Error {}

async function ask(
  client: Client,
  remoteNode: string,
  input: ReuseCheckInput,
  selection: {
    outputsFromRunId?: string;
    outputsFromRuns?: Record<string, string>;
  }
): Promise<StepReuseCheck> {
  const common = {
    steps: [input.step],
    ...selection,
    ...(input.profile !== undefined ? { profile: input.profile } : {}),
  };
  const { data, error, response } =
    input.draft === null
      ? await client.POST('/dags/{fileName}/start-preview', {
          params: { path: { fileName: input.fileName }, query: { remoteNode } },
          body: common,
        })
      : await client.POST('/dag-runs/preview', {
          params: { query: { remoteNode } },
          body: {
            ...common,
            spec: input.draft,
            name: input.dagName,
            draftOf: input.fileName,
          },
        });
  if (error === undefined && data) return data;
  if (
    (response.status === 404 || response.status === 405) &&
    !isApiError(error)
  ) {
    throw new Unsupported();
  }
  throw new Error(errorMessage(error, response.status));
}

async function check(
  client: Client,
  remoteNode: string,
  input: ReuseCheckInput
): Promise<Answer> {
  try {
    const entries = { ...input.entries };
    const [source, cache] = await Promise.all([
      input.sourceRunId !== null
        ? ask(client, remoteNode, input, {
            outputsFromRunId: input.sourceRunId,
          })
        : null,
      // A run with no cache entry still has a profile to name.
      Object.keys(entries).length > 0 || input.sourceRunId === null
        ? ask(client, remoteNode, input, { outputsFromRuns: entries })
        : null,
    ]);
    return {
      state: 'ready',
      fromSource: byStep(source),
      fromCache: byStep(cache),
      profile: (source ?? cache)?.profile ?? '',
      error: null,
    };
  } catch (e) {
    // A body that is not JSON comes from a server that has no such path.
    if (e instanceof Unsupported || e instanceof SyntaxError) {
      return { ...CHECKING, state: 'unsupported' };
    }
    return {
      ...CHECKING,
      state: 'failed',
      error: e instanceof Error && e.message ? e.message : String(e),
    };
  }
}

// The key of a check: its node and input, the entries in order, so that the
// same step run gives the same key. inputOf reads the input back.
function keyOf(remoteNode: string, input: ReuseCheckInput): string {
  const entries = Object.entries(input.entries).sort(([a], [b]) =>
    a < b ? -1 : a > b ? 1 : 0
  );
  return JSON.stringify([
    remoteNode,
    input.fileName,
    input.dagName,
    input.step,
    input.sourceRunId,
    entries,
    input.draft,
    input.profile ?? null,
  ]);
}

function inputOf(key: string): ReuseCheckInput {
  const [, fileName, dagName, step, sourceRunId, entries, draft, profile] =
    JSON.parse(key) as [
      string,
      string,
      string,
      string,
      string | null,
      [string, string][],
      string | null,
      string | null,
    ];
  return {
    fileName,
    dagName,
    step,
    sourceRunId,
    entries: Object.fromEntries(entries),
    draft,
    ...(profile !== null ? { profile } : {}),
  };
}

/**
 * Asks the server what changed for each previous step of a step run since
 * the run that would give its outputs, as the start would check it, with
 * the code of the start. The answer is kept for the page for
 * `VERDICT_FRESH_MS` and shared by every caller with the same input: the step
 * run dialog asks, and the INPUT column of the step reads it with
 * `ask: false`, so that a passive view never makes the server evaluate
 * parameters. It never polls.
 */
export function useReuseCheck(
  input: ReuseCheckInput,
  options: {
    enabled: boolean;
    ask: boolean;
    /** An answer older than this time (ms) does not stand. */
    since?: number;
  }
): ReuseCheck {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const key = keyOf(remoteNode, input);
  const stored = useSyncExternalStore(
    subscribe,
    () => verdicts.get(key),
    () => verdicts.get(key)
  );
  const { enabled, ask: asks, since = 0 } = options;

  useEffect(() => {
    if (
      !enabled ||
      !asks ||
      pending.has(key) ||
      fresh(verdicts.get(key), since)
    ) {
      return;
    }
    pending.add(key);
    void check(client, remoteNode, inputOf(key)).then((answer) => {
      pending.delete(key);
      publish(key, answer);
    });
  }, [asks, client, enabled, key, remoteNode, since, stored]);

  const retry = useCallback(() => {
    verdicts.delete(key);
    listeners.forEach((listener) => listener());
  }, [key]);
  const answer = fresh(stored, since) ? stored.answer : CHECKING;
  return { ...answer, retry };
}
