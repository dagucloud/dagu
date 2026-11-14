// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ApiDAGDetails } from '@/features/visual-editor/contracts';
import {
  coverFromCache,
  isActive,
  missingIn,
  nodeHoldsOutputs,
  paramsDiffer as paramsDiffer_,
  pickReuseSource,
  staleUpstream,
  stepNeeds,
  type ReusePick,
  type ReuseRun,
  type RunCheck,
} from '@/features/visual-editor/run/reuseSource';
import {
  buildSourceOptions,
  CACHE_OPTION_ID,
  runOptionId,
  type SourceOption,
  type SourceRun,
} from '@/features/visual-editor/run/runSourceOptions';
import {
  useSourceChoice,
  type SourceChoice,
} from '@/features/visual-editor/run/runSourceChoice';
import {
  useCacheScope,
  useOutputCacheEntries,
} from '@/features/visual-editor/run/useOutputCacheEntries';
import { useRunSpecDiff } from '@/features/visual-editor/run/useRunSpecStale';
import { useLiveStepHashes } from '@/features/visual-editor/run/useStepHashes';
import type { StepRunRef } from '@/features/visual-editor/run/inputProvenance';
import { useClient } from '@/hooks/api';
import { useWorkspaceData } from '../data/context';
import { useRunHistory } from '../data/useRunHistory';

/** The most requests that one evaluation makes to check the runs of the cache. */
const MAX_RUN_CHECKS = 5;

/** The most outputs requests that one evaluation makes. */
export const MAX_OUTPUT_REQUESTS = 3;

// The step that stands for the agent in the runs of an agent DAG.
const AGENT_RUN_STEP = '__agent__';

export interface ReuseSourceState {
  /**
   * The run that the step run copies outputs from: the run that the user
   * chose, else the automatic choice.
   */
  source: ReuseRun | null;
  /** The lines of the run picker, and the line that the step run uses now. */
  options: SourceOption[];
  selectedOptionId: string | null;
  /** The user chose a line of the picker. */
  choose(option: SourceOption): void;
  /** The active run that the step waits for when it has no source or cache. */
  waitingFor: string | null;
  /**
   * Previous steps that changed since the source run (parsed compare) and
   * that the cache does not cover.
   */
  stale: string[];
  /** Previous steps that neither the source run nor the cache covers. */
  missing: string[];
  /**
   * Previous steps that the cache covers: a fresh entry whose run still
   * exists and holds the step.
   */
  cached: string[];
  /**
   * The run that each cached step is read from, for the `outputsFromRuns` of
   * the request, and when it was captured (ms).
   */
  cachedRuns: Record<string, { runId: string; capturedAt: number }>;
  /** Previous steps with a fresh entry whose source run is no longer available. */
  gone: string[];
  /**
   * The run of each previous step that a run gives (the source run or the run
   * of the cache), with the times that the step had there.
   */
  stepRuns: Record<string, StepRunRef>;
  /** The oldest capture time of the cached steps (ms); null with none. */
  cacheCapturedAt: number | null;
  /** The capture time of each cached step (ms). */
  cacheTimes: Record<string, number>;
  /** A top-level key of the definition changed: every previous step is stale. */
  headChanged: boolean;
  /** The source run used parameters other than the defaults. */
  paramsDiffer: boolean;
  /** The source or its definition is not known yet. */
  loading: boolean;
  /**
   * The id that the history holds for a run id written in lower case (a run
   * id in a label), or null when the run is not in the history.
   */
  resolveRun(runId: string): string | null;
}

/**
 * The reuse source of a step run (decision D12) and what changed upstream of
 * it (D14). The user can choose the run (`choose`, kept for the session): a
 * chosen run that is gone or does not hold the reads gives way to the
 * automatic choice, and so does a chosen cache that no fresh entry backs.
 * The browser cache of outputs covers the steps that the
 * source run cannot give: a step that changed or has no output in it. A
 * cache entry names a run, never holds a value to send: it covers a step only
 * while that run exists (the history, else a request) and holds the step. The
 * history is the one of the dock timeline, with the test runs, which the user
 * can choose but the automatic choice never takes. The automatic source
 * is chosen again when the history, the selected run, the followed run or the
 * reads change, and never by polling. One evaluation makes at most
 * `MAX_OUTPUT_REQUESTS` outputs requests, and none when the step reads
 * nothing. The followed upstream run is read from the history or the
 * selected run; another run is ignored.
 */
export function useReuseSource(p: {
  fileName: string;
  step: string;
  previous: readonly string[];
  reads: readonly string[];
  followedUpstreamRunId: string | null;
  /** The editor text; the saved definition when undefined. */
  liveText?: string;
}): ReuseSourceState {
  const { dagData, run, remoteNode } = useWorkspaceData('useReuseSource');
  const client = useClient();
  const history = useRunHistory({ enabled: true, includeEphemeral: true });
  const dagName = dagData.dag?.name ?? p.fileName;
  // The automatic choice looks at the runs that stay; a test run is for the
  // user to choose.
  const keptRuns = useMemo(
    () => history.runs.filter((r) => !r.ephemeral),
    [history.runs]
  );

  const selected = run.details;
  const selectedRef = useRef(selected);
  selectedRef.current = selected;
  const clientRef = useRef(client);
  clientRef.current = client;

  const readsKey = p.reads.join('\u0000');
  const selectedKey = selected ? `${selected.dagRunId}:${selected.status}` : '';
  const evaluation = `${dagName}\u0001${remoteNode}\u0001${readsKey}\u0001${p.followedUpstreamRunId ?? ''}\u0001${selectedKey}`;

  const [picked, setPicked] = useState<{
    key: string;
    runs: unknown;
    pick: ReusePick;
  } | null>(null);

  useEffect(() => {
    let cancelled = false;
    const reads = readsKey === '' ? [] : readsKey.split('\u0000');
    const runs: ReuseRun[] = keptRuns;
    const allRuns: ReuseRun[] = history.runs;
    const selectedNow = selectedRef.current;
    const followed =
      p.followedUpstreamRunId === null
        ? null
        : // The run of the upstream steps of a draft is a test run.
          (allRuns.find((r) => r.dagRunId === p.followedUpstreamRunId) ??
          (selectedNow?.dagRunId === p.followedUpstreamRunId
            ? selectedNow
            : null));
    let budget = MAX_OUTPUT_REQUESTS;
    // The nodes of the run already show the reads as complete. The request
    // confirms that the outputs exist; a run that the budget cannot cover
    // stays trusted on its nodes.
    const holds = async (candidate: ReuseRun): Promise<boolean> => {
      if (reads.length === 0 || reads.length > budget) return true;
      budget -= reads.length;
      try {
        const answers = await Promise.all(
          reads.map((stepName) =>
            clientRef.current.GET(
              '/dag-runs/{name}/{dagRunId}/steps/{stepName}/outputs',
              {
                params: {
                  path: {
                    name: dagName,
                    dagRunId: candidate.dagRunId,
                    stepName,
                  },
                  query: { remoteNode },
                },
              }
            )
          )
        );
        return answers.every(
          ({ data }) =>
            !!data && (data.outputs.length > 0 || !!data.outputVariable)
        );
      } catch {
        return false;
      }
    };
    void pickReuseSource({
      followedUpstream: followed,
      // A test run is never the automatic source: the user can choose it.
      selected: selectedNow?.ephemeral ? null : selectedNow,
      history: runs,
      reads,
      holds,
    }).then((pick) => {
      if (!cancelled) setPicked({ key: evaluation, runs: keptRuns, pick });
    });
    return () => {
      cancelled = true;
    };
    // The selected run is read through a ref; its id and status are in the
    // key. The live text does not change the choice.
  }, [evaluation, keptRuns]);

  // A pick of an older history stays until the new evaluation ends, so the
  // source does not blink away when the history changes.
  const current = picked?.key === evaluation ? picked : null;
  const autoSource = current?.pick.source ?? null;

  const scope = useCacheScope(p.fileName);
  const [choice, setChoice] = useSourceChoice(scope);

  const previous = useMemo(
    () => p.previous.filter((name) => name !== p.step),
    [p.previous, p.step]
  );

  // The cache: the entries of the workflow, and the hash that each previous
  // step has in the definition that the editor holds now. The definition is
  // read only when an entry exists.
  const entries = useOutputCacheEntries(scope);
  const hasEntries = Object.keys(entries).length > 0;
  const hashes = useLiveStepHashes({
    fileName: p.fileName,
    ...(p.liveText !== undefined ? { liveText: p.liveText } : {}),
    ...(dagData.dag ? { dag: dagData.dag as ApiDAGDetails } : {}),
    enabled: hasEntries,
  });

  // The runs that the entries and the choice name must still exist. The
  // history says so for the runs that it lists; the others are asked once (a
  // removed run, or a test run that expired, answers 404).
  const [asked, setAsked] = useState<
    Record<string, ReuseRun | 'gone' | 'unknown'>
  >({});
  const runIds = useMemo(
    () => [
      ...new Set([
        ...(choice.kind === 'run' ? [choice.runId] : []),
        ...previous.flatMap((name) =>
          entries[name] ? [entries[name].runId] : []
        ),
      ]),
    ],
    [choice, entries, previous]
  );
  const listed = history.runs;
  const runOfId = useCallback(
    (id: string): ReuseRun | 'gone' | 'unknown' | undefined =>
      listed.find((r) => r.dagRunId === id) ??
      (selected?.dagRunId === id ? selected : undefined) ??
      asked[id],
    [asked, listed, selected]
  );
  // Each run is asked once. A pass never drops the answers of an earlier
  // one, and the pass after an answer asks for the runs past the limit.
  const requested = useRef(new Set<string>());
  useEffect(() => {
    if (history.isLoading) return;
    const missingIds = runIds.filter(
      (id) => !requested.current.has(id) && runOfId(id) === undefined
    );
    for (const id of missingIds.slice(0, MAX_RUN_CHECKS)) {
      requested.current.add(id);
      void clientRef.current
        .GET('/dag-runs/{name}/{dagRunId}', {
          params: {
            path: { name: dagName, dagRunId: id },
            query: { remoteNode },
          },
        })
        .then(({ data, response }) => {
          // Only a clear "not found" says that the run is gone.
          // Another failure leaves the decision to the server.
          const answer: ReuseRun | 'gone' | 'unknown' = data
            ? data.dagRunDetails
            : response.status === 404
              ? 'gone'
              : 'unknown';
          setAsked((held) => ({ ...held, [id]: answer }));
        })
        .catch(() => {
          setAsked((held) => ({ ...held, [id]: 'unknown' }));
        });
    }
  }, [dagName, history.isLoading, remoteNode, runIds, runOfId]);
  const check = useCallback(
    (step: string, entry: { runId: string }): RunCheck => {
      const found = runOfId(entry.runId);
      if (found === undefined) return 'checking';
      if (found === 'gone') return 'gone';
      if (found === 'unknown') return 'ok';
      if (isActive(found)) return 'active';
      const node = (found.nodes ?? []).find((n) => n.step.name === step);
      return nodeHoldsOutputs(node) ? 'ok' : 'gone';
    },
    [runOfId]
  );

  // The run that the user chose, when it still exists and holds the reads.
  const chosen = choice.kind === 'run' ? runOfId(choice.runId) : null;
  const chosenRun =
    typeof chosen === 'object' &&
    chosen !== null &&
    !isActive(chosen) &&
    missingIn(chosen, p.reads).length === 0
      ? chosen
      : null;

  // The cached values that the picker offers: the fresh entries, with the
  // runs that they were read from.
  const offered = useMemo(
    () =>
      coverFromCache({
        previous,
        stale: [],
        missing: [],
        hasSource: false,
        entries,
        hashes,
        check,
      }),
    [check, entries, hashes, previous]
  );

  // A choice of the cache gives way to the automatic choice once no fresh
  // entry backs it; it holds while the entries are checked.
  const cacheChosen =
    choice.kind === 'cache' &&
    (offered.cached.length > 0 ||
      offered.checking ||
      (hasEntries && hashes === null));
  const source = cacheChosen ? null : (chosenRun ?? autoSource);

  const names = useMemo(() => {
    if (!source?.nodes) return undefined;
    return {
      snapshot: {
        name: dagName,
        steps: source.nodes
          .map((node) => node.step)
          .filter((step) => step.name !== AGENT_RUN_STEP),
      } as unknown as ApiDAGDetails,
      live: dagData.dag as ApiDAGDetails | undefined,
    };
  }, [dagData.dag, dagName, source]);

  const diff = useRunSpecDiff({
    dagName,
    fileName: p.fileName,
    runId: source?.dagRunId ?? null,
    ...(p.liveText !== undefined ? { liveText: p.liveText } : {}),
    ...(names ? { names } : {}),
  });

  const deps = useMemo(() => stepNeeds(dagData.dag?.steps), [dagData.dag]);
  const runStale = useMemo(
    () => (diff && source ? staleUpstream({ diff, previous, deps }) : []),
    [deps, diff, previous, source]
  );
  const runMissing = useMemo(
    () => missingIn(source, previous),
    [source, previous]
  );

  const cover = useMemo(
    () =>
      coverFromCache({
        previous,
        stale: runStale,
        missing: runMissing,
        hasSource: source !== null,
        entries,
        hashes,
        check,
      }),
    [check, entries, hashes, previous, runMissing, runStale, source]
  );
  const options = useMemo(() => {
    const runs: SourceRun[] = [...history.runs];
    // The chosen run, or the selected one, can be older than the history.
    for (const extra of [chosenRun, source, autoSource]) {
      if (extra && !runs.some((r) => r.dagRunId === extra.dagRunId)) {
        runs.push(extra);
      }
    }
    const cacheRuns = [
      ...new Set(offered.cached.map((name) => offered.runs[name]!.runId)),
    ];
    return buildSourceOptions({
      runs,
      auto: autoSource,
      reads: p.reads,
      cache:
        cacheRuns.length > 0 && offered.capturedAt !== null
          ? { runIds: cacheRuns, capturedAt: offered.capturedAt }
          : null,
    });
  }, [history.runs, chosenRun, source, autoSource, offered, p.reads]);
  const selectedOptionId = cacheChosen
    ? offered.cached.length > 0
      ? CACHE_OPTION_ID
      : null
    : source
      ? runOptionId(source.dagRunId)
      : null;
  const choose = useCallback(
    (option: SourceOption) => {
      const next: SourceChoice = option.auto
        ? { kind: 'auto' }
        : option.kind === 'cache'
          ? { kind: 'cache' }
          : { kind: 'run', runId: option.runId ?? '' };
      setChoice(next);
    },
    [setChoice]
  );

  // The run of each reused step, with the times that the step had there: the
  // dialog lists them, and the record of the request carries them.
  const stepRuns = useMemo(() => {
    const result: Record<string, StepRunRef> = {};
    const refOf = (runId: string, run: ReuseRun | undefined, step: string) => {
      const node = run?.nodes?.find((n) => n.step.name === step);
      result[step] = {
        runId,
        ...(node?.startedAt && node.startedAt !== '-'
          ? { startedAt: node.startedAt }
          : {}),
        ...(node?.finishedAt && node.finishedAt !== '-'
          ? { finishedAt: node.finishedAt }
          : {}),
      };
    };
    for (const step of previous) {
      const own = cover.runs[step];
      if (own) {
        const found = runOfId(own.runId);
        refOf(
          own.runId,
          found === 'gone' || found === 'unknown' ? undefined : found,
          step
        );
      } else if (source && !runMissing.includes(step)) {
        refOf(source.dagRunId, source, step);
      }
    }
    return result;
  }, [cover.runs, previous, runMissing, runOfId, source]);

  const historyRuns = history.runs;
  const resolveRun = useCallback(
    (id: string) =>
      historyRuns.find((r) => r.dagRunId.toLowerCase() === id.toLowerCase())
        ?.dagRunId ?? null,
    [historyRuns]
  );

  return {
    source,
    options,
    selectedOptionId,
    choose,
    resolveRun,
    waitingFor:
      source || cacheChosen ? null : (current?.pick.waitingFor ?? null),
    stale: cover.stale,
    missing: cover.missing,
    cached: cover.cached,
    cachedRuns: cover.runs,
    gone: cover.gone,
    stepRuns,
    cacheCapturedAt: cover.capturedAt,
    cacheTimes: cover.times,
    headChanged: diff?.headChanged ?? false,
    paramsDiffer: source
      ? paramsDiffer_(source.params, dagData.dag?.defaultParams)
      : false,
    loading:
      current === null ||
      current.runs !== keptRuns ||
      history.isLoading ||
      chosen === undefined ||
      (source !== null && diff === null) ||
      (hasEntries && hashes === null) ||
      cover.checking ||
      offered.checking,
  };
}
