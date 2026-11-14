// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect } from 'react';
import { Status } from '@/api/v1/schema';
import { useWorkspaceRun } from '../data';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { useDockFrame } from './DockContext';

// Runs that the rules handled in this page session. Never stored.
const handledRuns = new Set<string>();

/**
 * Opens the dock by itself, only when a run that the user started in this tab
 * fails, at most once for each run. It opens Log on the first failed step
 * with stderr and never writes the URL. A run that is loaded failed, a run
 * that starts and a run that waits for work do not open the dock. A run seen
 * while ?step or ?dock is set is left alone.
 */
export function useDockAutoOpen(): void {
  const run = useWorkspaceRun();
  const [url] = useWorkspaceUrl();
  const { facts, showAuto, startedRuns } = useDockFrame();

  const runId = run.ref?.runId ?? null;
  const runKey = run.ref ? `${run.ref.name}\n${run.ref.runId}` : null;
  const startedHere = runId !== null && startedRuns.has(runId);
  const linked = !!(url.step || url.dock);
  const failed = run.details?.status === Status.Failed;
  const failedStep = run.failedStep;

  useEffect(() => {
    if (!runKey || !facts || !startedHere || !failed) return;
    if (handledRuns.has(runKey)) return;
    handledRuns.add(runKey);
    if (linked) return;
    showAuto('log', failedStep ? { step: failedStep, stream: 'stderr' } : null);
  }, [runKey, facts, startedHere, linked, failed, failedStep, showAuto]);
}
