// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Status } from '@/api/v1/schema';
import { getManualActionState } from '@/features/dag-runs/lib/manualActionState';
import {
  fallbackRunTab,
  runViewFacts,
  visibleRunTabs,
  type RunViewFacts,
  type RunViewTab,
} from '@/features/dags/lib/runViewTabs';
import type { ApiDAGRunDetails } from '../data/types';
import type { DockTab } from '../state/workspaceUrl';

/** The run view facts, with the facts that only the dock uses. */
export interface DockFacts extends RunViewFacts {
  /** A waiting run whose approval was saved but whose resume failed. */
  approvalResumePending?: boolean;
  /** A waiting run whose human tasks were saved but whose resume failed. */
  humanTaskResumePending?: boolean;
  /** An agent DAG run: it has agent events or agent tasks. */
  isAgentRun?: boolean;
  /** The run has a continuous step: a step that repeats without a limit. */
  hasContinuous?: boolean;
  /** Steps that wait while a run that they called waits for a person. */
  subRunsWaiting?: number;
}

/** The facts of a run for the dock tabs. */
export function dockFacts(
  details: ApiDAGRunDetails,
  opts: { artifactEnabled: boolean }
): DockFacts {
  return {
    ...runViewFacts(details, opts),
    approvalResumePending:
      details.status === Status.Waiting && !!details.approvalResumePending,
    humanTaskResumePending:
      details.status === Status.Waiting && !!details.humanTaskResumePending,
    isAgentRun:
      (details.agentEvents ?? []).length > 0 ||
      (details.agentTasks ?? []).length > 0,
    hasContinuous: (details.nodes ?? []).some((n) => n.continuous === true),
    subRunsWaiting: getManualActionState(details).waitingSubRunNodes.length,
  };
}

/**
 * The dock tab that shows a run view, or null when the Run panel has none.
 * The status view is the Log tab (the steps are the left navigation), and
 * the spec is the Definition tab. Human tasks and approvals share Actions.
 */
export function toDockTab(t: RunViewTab): DockTab | null {
  switch (t) {
    case 'status':
      return 'log';
    case 'spec':
      return 'definition';
    case 'human-tasks':
    case 'approval':
      return 'actions';
    default:
      return t;
  }
}

/** The tab that opens when nothing chose one: the log of a step, else the timeline. */
export function defaultDockTab(opts: {
  stepShown: boolean;
  failed: boolean;
}): DockTab {
  return opts.stepShown || opts.failed ? 'log' : 'timeline';
}

/**
 * Returns the dock tabs for the facts of the selected run, in strip order and
 * without duplicates. 'log' comes first for every run. A workflow that never
 * ran (facts null) has only 'log'. While the selected run loads (`loading`)
 * History stays: it reads the run list, and a pick in it loads another run.
 */
export function dockTabsFor(
  facts: DockFacts | null,
  opts: { loading?: boolean } = {}
): DockTab[] {
  if (!facts) return opts.loading ? ['log', 'history'] : ['log'];
  const runTabs = visibleRunTabs(facts);
  // The Agent tab holds the timeline of an agent DAG run, which has no
  // agent session on its nodes.
  if (facts.isAgentRun && !runTabs.includes('agent')) {
    runTabs.splice(1, 0, 'agent');
  }
  // Actions holds the Retry resume of a failed approval resume, and the
  // actions that wait in the runs that the steps called.
  if (
    (facts.approvalResumePending || (facts.subRunsWaiting ?? 0) > 0) &&
    !runTabs.includes('human-tasks') &&
    !runTabs.includes('approval')
  ) {
    // After 'status' and 'agent', as visibleRunTabs orders them.
    runTabs.splice(runTabs.includes('agent') ? 2 : 1, 0, 'approval');
  }
  const tabs: DockTab[] = [];
  for (const runTab of runTabs) {
    const tab = toDockTab(runTab);
    if (tab && !tabs.includes(tab)) tabs.push(tab);
  }
  // History sits before Outputs: it is about the runs, not one run.
  tabs.splice(tabs.indexOf('outputs'), 0, 'history');
  // Continuous follows the Log tab: the step log is its raw record.
  if (facts.hasContinuous)
    tabs.splice(tabs.indexOf('log') + 1, 0, 'continuous');
  return tabs;
}

/**
 * Waiting counts shown on the Agent and Actions tabs; a failed resume counts
 * as one action. Zero counts are left out.
 */
export function dockTabBadges(
  facts: DockFacts | null
): Partial<Record<DockTab, number>> {
  const badges: Partial<Record<DockTab, number>> = {};
  if (!facts) return badges;
  if (facts.agentWaiting > 0) badges.agent = facts.agentWaiting;
  const actions =
    facts.waitingApprovals +
    facts.humanTasksWaiting +
    (facts.subRunsWaiting ?? 0) +
    (facts.approvalResumePending ? 1 : 0) +
    (facts.humanTaskResumePending ? 1 : 0);
  if (actions > 0) badges.actions = actions;
  return badges;
}

/** Returns active when it is visible, else 'log' (fallbackRunTab of the dock). */
export function fallbackDockTab(
  active: DockTab,
  visible: readonly DockTab[]
): DockTab {
  // fallbackRunTab only tests membership, so dock tab names pass through it.
  const kept = fallbackRunTab(
    active as RunViewTab,
    visible as readonly string[] as readonly RunViewTab[]
  );
  return kept === (active as string) ? active : (toDockTab(kept) ?? 'log');
}
