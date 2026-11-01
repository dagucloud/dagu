// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import { Status } from '@/api/v1/schema';
import { useMonitoringOfferState } from '@/components/MonitoringOffer';
import { useI18n } from '@/i18n/I18nProvider';
import type { WorkspaceNotice } from '../contracts';
import { useWorkspaceRun } from '../data';
import { useWorkspaceData } from '../data/context';
import { isNewerRun } from '../data/runFacts';
import { useDockCommands } from '../dock/DockContext';

/** The highest priority shows first. DAGSpec ranks its own notices around these. */
export const RUN_NOTICE_PRIORITY = {
  'run-not-found': 60,
  'approval-resume': 50,
  'agent-waiting': 45,
  'actions-waiting': 40,
  'newer-run-active': 30,
  'monitoring-offer': 10,
} as const;

// The statuses for which isRunActive is true.
const ACTIVE_STATUSES: ReadonlySet<Status> = new Set([
  Status.NotStarted,
  Status.Queued,
  Status.Running,
  Status.Waiting,
]);

/**
 * Run-level notices of the workspace, highest priority first. DAGSpec adds
 * draft, frozen, edit-refused and validation itself.
 */
export function useWorkspaceNotices(): WorkspaceNotice[] {
  const { ts } = useI18n();
  const navigate = useNavigate();
  const run = useWorkspaceRun();
  const { dagData } = useWorkspaceData('useWorkspaceNotices');
  const dock = useDockCommands();
  const offer = useMonitoringOfferState();

  const { notFound, hasRun, selection, latestRef, waiting, select } = run;
  const status = run.details?.status;
  const resumePending =
    status === Status.Waiting && !!run.details?.approvalResumePending;
  const actionCount = waiting.approvals + waiting.humanTasks;
  const agentWaiting = waiting.agent > 0;
  const pinnedRunId = selection === 'latest' ? null : selection.runId;
  const selectedRun = run.details;
  const newerRunActive =
    pinnedRunId !== null &&
    latestRef !== null &&
    latestRef.runId !== pinnedRunId &&
    ACTIVE_STATUSES.has(latestRef.status) &&
    (selectedRun === null || isNewerRun(latestRef, selectedRun));
  // Scheduled runs happen unattended, which is when alerts matter.
  const runsOnSchedule = !!dagData.dag?.schedule?.length && !dagData.suspended;
  const offerVisible = runsOnSchedule && offer.visible;
  const { setTab } = dock;
  const { dismiss: dismissOffer } = offer;

  return useMemo(() => {
    const notices: WorkspaceNotice[] = [];
    // With no run at all, 'the latest run is shown' would be false.
    if (notFound && hasRun) {
      notices.push({
        id: 'run-not-found',
        tone: 'warning',
        message: ts(
          'The run in the link was not found. The latest run is shown.'
        ),
        dismissible: true,
        dismiss: () => select('latest', { replace: true }),
        priority: RUN_NOTICE_PRIORITY['run-not-found'],
      });
    }
    if (resumePending) {
      notices.push({
        id: 'approval-resume',
        tone: 'warning',
        message: ts('Approval saved; resume failed.'),
        action: {
          label: ts('Open actions'),
          run: () => setTab('actions', { user: true }),
        },
        priority: RUN_NOTICE_PRIORITY['approval-resume'],
      });
    }
    if (agentWaiting) {
      notices.push({
        id: 'agent-waiting',
        tone: 'info',
        message: ts('An agent is waiting for an answer.'),
        action: {
          label: ts('Open agent'),
          run: () => setTab('agent', { user: true }),
        },
        priority: RUN_NOTICE_PRIORITY['agent-waiting'],
      });
    }
    if (actionCount > 0) {
      notices.push({
        id: 'actions-waiting',
        tone: 'info',
        message:
          actionCount === 1
            ? ts('An action is waiting.')
            : ts('{count} actions are waiting.', { count: actionCount }),
        action: {
          label: ts('Open actions'),
          run: () => setTab('actions', { user: true }),
        },
        priority: RUN_NOTICE_PRIORITY['actions-waiting'],
      });
    }
    if (newerRunActive) {
      notices.push({
        id: 'newer-run-active',
        tone: 'info',
        message: ts('A newer run is active'),
        action: { label: ts('Follow latest'), run: () => select('latest') },
        priority: RUN_NOTICE_PRIORITY['newer-run-active'],
      });
    }
    if (offerVisible) {
      notices.push({
        id: 'monitoring-offer',
        tone: 'info',
        message: ts('Get an email if this server stops'),
        action: {
          label: ts('Set up for free'),
          run: () => navigate('/license#monitoring'),
        },
        dismissible: true,
        dismiss: dismissOffer,
        priority: RUN_NOTICE_PRIORITY['monitoring-offer'],
      });
    }
    return notices;
  }, [
    ts,
    navigate,
    select,
    setTab,
    dismissOffer,
    notFound,
    hasRun,
    resumePending,
    agentWaiting,
    actionCount,
    newerRunActive,
    offerVisible,
  ]);
}
