// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Status } from '@/api/v1/schema';
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
  /** An agent DAG run: it has agent events or agent tasks. */
  isAgentRun?: boolean;
  /** The run has a continuous step: a step that repeats without a limit. */
  hasContinuous?: boolean;
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
    isAgentRun:
      (details.agentEvents ?? []).length > 0 ||
      (details.agentTasks ?? []).length > 0,
    hasContinuous: (details.nodes ?? []).some((n) => n.continuous === true),
  };
}

/**
 * The dock tab that shows a run view, or null when the Run panel has none.
 * The status view is the Log tab (the steps are the left navigation), and
 * the spec is not a tab. Human tasks and approvals share Actions.
 */
export function toDockTab(t: RunViewTab): DockTab | null {
  switch (t) {
    case 'status':
      return 'log';
    case 'spec':
      return null;
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
 * ran (facts null) has only 'log'.
 */
export function dockTabsFor(facts: DockFacts | null): DockTab[] {
  if (!facts) return ['log'];
  const runTabs = visibleRunTabs(facts);
  // The Agent tab holds the timeline of an agent DAG run, which has no
  // agent session on its nodes.
  if (facts.isAgentRun && !runTabs.includes('agent')) {
    runTabs.splice(1, 0, 'agent');
  }
  // Actions holds the Retry resume of a failed approval resume.
  if (
    facts.approvalResumePending &&
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
  // Continuous follows the Log tab: the step log is its raw record.
  if (facts.hasContinuous)
    tabs.splice(tabs.indexOf('log') + 1, 0, 'continuous');
  return tabs;
}

/** Waiting counts shown on the Agent and Actions tabs. Zero counts are left out. */
export function dockTabBadges(
  facts: RunViewFacts | null
): Partial<Record<DockTab, number>> {
  const badges: Partial<Record<DockTab, number>> = {};
  if (!facts) return badges;
  if (facts.agentWaiting > 0) badges.agent = facts.agentWaiting;
  const actions = facts.waitingApprovals + facts.humanTasksWaiting;
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
