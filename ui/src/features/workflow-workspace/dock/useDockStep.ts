// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useWorkspaceRun } from '../data';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { useDock } from './DockContext';

/**
 * The step that the Run panel shows: ?step, else the step that the auto-open
 * chose for a run that has it. null is the run view (the summary row).
 * The steps list, the tab strip and the tab bodies read this one value.
 */
export function useDockStep(): string | null {
  const run = useWorkspaceRun();
  const [url] = useWorkspaceUrl();
  const { autoStep } = useDock();
  if (url.step !== null) return url.step;
  return autoStep && run.nodeByStep.has(autoStep.step) ? autoStep.step : null;
}
