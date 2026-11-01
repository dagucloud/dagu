// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ApiDAGRunDetails } from '../../contracts';
import { HANDLER_EVENTS } from '../../data/handlerMapping';
import { dockFacts, dockTabsFor } from '../dockTabs';
import type { DockTab } from '../../state/workspaceUrl';

/**
 * The label of a tab in a step view, where it differs from the run view: the
 * run view lists the outputs of every step (Outputs), a step view only the
 * output of its step (Output).
 */
export const STEP_TAB_LABELS: Partial<Record<DockTab, string>> = {
  outputs: 'Output',
};

/**
 * The tabs of a step view: the tabs of the run view without the Timeline,
 * which belongs to the run view only. Every host of a step view reads this
 * one rule: the dock with a step selected and the right pane of the step
 * edit view.
 */
export function stepViewTabs(tabs: readonly DockTab[]): DockTab[] {
  return tabs.filter((tab) => tab !== 'timeline');
}

/**
 * The run as one step sees it: the node of the step and nothing else of the
 * steps. Every tab body and the tab strip of a host that shows one step read
 * this view, so that the scope of a step view is one rule in one place.
 *
 * It keeps the node of the step (and its handler slot, when the step is a
 * handler), so the Outputs tab shows only that step, the Agent and Chat tabs
 * only its session, and the Actions tab only its approval or human task. The
 * run level data that no step owns is cleared: the agent events and tasks
 * and the artifacts, which the API does not tie to a step. The resume flags
 * stay only for a step that holds an approval or a human task.
 */
export function scopeDetailsToStep(
  details: ApiDAGRunDetails,
  step: string
): ApiDAGRunDetails {
  const node = (details.nodes ?? []).find((n) => n.step.name === step);
  const handler = HANDLER_EVENTS.find(
    (field) => details[field]?.step.name === step
  );
  const owner = node ?? (handler ? details[handler] : undefined);
  const manual =
    owner?.step.approval !== undefined || owner?.step.humanTask !== undefined;
  const scoped: ApiDAGRunDetails = {
    ...details,
    nodes: node ? [node] : [],
    artifactsAvailable: false,
    approvalResumePending: manual ? details.approvalResumePending : false,
    humanTaskResumePending: manual ? details.humanTaskResumePending : false,
  };
  // Absent, not empty: a run with no list is not an agent run.
  delete scoped.agentEvents;
  delete scoped.agentTasks;
  for (const field of HANDLER_EVENTS) {
    if (field !== handler) delete scoped[field];
  }
  return scoped;
}

/**
 * The tabs of the OUTPUT column for one step of a run: the tabs of the Run
 * panel for the scoped run (scopeDetailsToStep) without the Timeline
 * (stepViewTabs). A tab that has nothing for the step is not in the list, so
 * no tab shows empty or shows another step.
 */
export function stepDockTabs(
  details: ApiDAGRunDetails,
  step: string
): DockTab[] {
  const scoped = scopeDetailsToStep(details, step);
  return stepViewTabs(
    dockTabsFor(dockFacts(scoped, { artifactEnabled: false }))
  );
}
