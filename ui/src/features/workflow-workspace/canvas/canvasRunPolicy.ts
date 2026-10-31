// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useMemo, useRef, useState } from 'react';
import { subscribeClock } from '@/hooks/useClock';
import type { RunSource } from '@/features/visual-editor/contracts';
import { parseDagDocLenient } from '@/features/visual-editor/doc';
import type {
  ApiDAGRunDetails,
  WorkspaceEditorHandle,
  WorkspaceRunValue,
} from '../contracts';

/**
 * The run that the editor canvas shows. It is the run that the workspace
 * selected: ?run=X shows run X, and no ?run (before the redirect to the latest run) shows the latest run.
 *
 * This is the only place that decides it. The other options of owner
 * question 1 (a canvas that shows another run, or no run until the user
 * picks one) change only this function.
 */
export function canvasRunSource(
  run: Pick<WorkspaceRunValue, 'ref' | 'details'>
): RunSource {
  return { runId: run.ref?.runId ?? null, details: run.details };
}

/** The steps of the draft that a status can safely follow. */
export interface DraftStepIdentity {
  names: ReadonlySet<string>;
  ids: ReadonlySet<string>;
}

/**
 * The explicit names and ids of the steps of a draft, or null when the text
 * has no readable steps (the canvas then keeps its last valid version).
 */
export function draftStepIdentity(text: string): DraftStepIdentity | null {
  const model = parseDagDocLenient(text);
  if (!model.ok && model.steps.length === 0) return null;
  const names = new Set<string>();
  const ids = new Set<string>();
  for (const step of model.steps) {
    if (step.explicitName !== undefined) names.add(step.explicitName);
    if (step.id !== undefined) ids.add(step.id);
  }
  return { names, ids };
}

function sameSet(a: ReadonlySet<string>, b: ReadonlySet<string>): boolean {
  if (a.size !== b.size) return false;
  for (const value of a) if (!b.has(value)) return false;
  return true;
}

function sameIdentity(a: DraftStepIdentity, b: DraftStepIdentity): boolean {
  return sameSet(a.names, b.names) && sameSet(a.ids, b.ids);
}

/**
 * Drops the statuses that a draft edit can put on the wrong card. The server
 * numbers a step with no name and no id by its position (`cmd_2`), so an
 * insert, a delete or a move in the draft shifts those names. A step that
 * the draft names or gives an id keeps its status. Without a draft
 * (`identity` is null) nothing drops, and the same source comes back when
 * nothing drops.
 */
export function withStableSteps(
  source: RunSource,
  identity: DraftStepIdentity | null
): RunSource {
  const nodes = source.details?.nodes;
  if (!identity || !source.details || !nodes) return source;
  const kept = nodes.filter(
    (node) =>
      identity.names.has(node.step.name) ||
      (node.step.id !== undefined && identity.ids.has(node.step.id))
  );
  if (kept.length === nodes.length) return source;
  return { runId: source.runId, details: { ...source.details, nodes: kept } };
}

const DRAFT_POLL_MS = 500;

/**
 * The step identity of the draft, while the draft differs from the saved
 * file; null otherwise. The editor gives no event for each edit, so the
 * buffer is read at a short interval and parsed only when its text changed.
 */
export function useDraftStepIdentity(
  handle: Pick<WorkspaceEditorHandle, 'getBuffer'> | null,
  dirty: boolean
): DraftStepIdentity | null {
  const [identity, setIdentity] = useState<DraftStepIdentity | null>(null);
  useEffect(() => {
    if (!handle || !dirty) {
      setIdentity(null);
      return;
    }
    let last: string | null = null;
    const read = () => {
      const text = handle.getBuffer();
      if (text === last) return;
      last = text;
      const next = draftStepIdentity(text);
      setIdentity((prev) =>
        prev && next && sameIdentity(prev, next) ? prev : next
      );
    };
    read();
    return subscribeClock(DRAFT_POLL_MS, read);
  }, [handle, dirty]);
  return identity;
}

// Every change of the run details rebuilds the graph of the editor: a live run
// reaches it at most once for each interval, and a new run reaches it at once.
const OVERLAY_INTERVAL_MS = 1000;

/** The details that the canvas shows: at most one change for each interval. */
export function useThrottledDetails(
  runId: string | null,
  details: ApiDAGRunDetails | null
): ApiDAGRunDetails | null {
  const [shown, setShown] = useState({ runId, details });
  const lastPush = useRef(0);
  useEffect(() => {
    const sameRun = shown.runId === runId;
    const wait = sameRun
      ? Math.max(0, lastPush.current + OVERLAY_INTERVAL_MS - Date.now())
      : 0;
    const timer = window.setTimeout(() => {
      lastPush.current = Date.now();
      setShown({ runId, details });
    }, wait);
    return () => window.clearTimeout(timer);
  }, [runId, details, shown.runId]);
  return shown.runId === runId ? shown.details : null;
}

/**
 * The run source of the editor canvas: the selected run of the workspace,
 * throttled so that a live run does not rebuild the graph on every event,
 * without the statuses that a draft edit would misplace.
 */
export function useCanvasRunSource(
  run: Pick<WorkspaceRunValue, 'ref' | 'details'>,
  handle: Pick<WorkspaceEditorHandle, 'getBuffer'> | null,
  dirty: boolean
): RunSource {
  const identity = useDraftStepIdentity(handle, dirty);
  const { runId, details } = canvasRunSource(run);
  const shown = useThrottledDetails(runId, details);
  // The guard comes after the throttle: an edit of the draft acts at once.
  return useMemo(
    () => withStableSteps({ runId, details: shown }, identity),
    [runId, shown, identity]
  );
}
