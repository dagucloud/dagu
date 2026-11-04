// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ClipboardCheck, FileInput } from 'lucide-react';
import { ActionButton } from '@/components/ui/action-button';
import { getManualActionState } from '@/features/dag-runs/lib/manualActionState';
import { useMemo, useState } from 'react';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { IconButton } from '@/components/ui/icon-button';
import DAGRunActions from '@/features/dag-runs/components/common/DAGRunActions';
import { useRunSpecStale } from '@/features/visual-editor/run/useRunSpecStale';
import { useI18n } from '@/i18n/I18nProvider';
import type { BarSlotProps } from '../contracts';
import { useWorkspacePermissions } from '../data/useWorkspacePermissions';
import { useWorkspaceRun } from '../data/useWorkspaceRun';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { useDockCommands } from '../dock/DockContext';
import { EditRetryDialog } from './EditRetryDialog';
import { useCopyRunSpecToEditor } from './useCopyRunSpecToEditor';
import { useShowRun } from './useShowRun';
import { cn } from '@/lib/utils';

/**
 * Flat icons for the selected run, past or active: stop, cancel auto-retry,
 * reject, retry, reschedule and dequeue (with runDags); then "Copy to
 * editor" (with write access, only while the run used a different definition
 * than the saved one). Each group has its slot of one icon, there, empty,
 * when the group has no icon: the bar does not change its width with the
 * state of the run. Absent for a workflow that never ran.
 */
export function RunActionsCluster({ fileName, dag }: BarSlotProps) {
  const { ts } = useI18n();
  const { canRunDags, canWrite } = useWorkspacePermissions();
  const run = useWorkspaceRun();
  const showRun = useShowRun();
  const [url, setUrl] = useWorkspaceUrl();
  const stopAll = useMemo(() => ({ fileName }), [fileName]);
  const [editRetry, setEditRetry] = useState(false);
  const dock = useDockCommands();
  const runId = run.ref?.runId ?? null;
  const stale = useRunSpecStale({
    dagName: run.ref?.name ?? dag.name,
    fileName,
    runId,
  });
  // The run definition view ends when its text is in the editor.
  const copy = useCopyRunSpecToEditor(fileName, () => {
    if (url.def === 'run') setUrl({ def: null }, { replace: true });
  });
  if (!run.ref || !run.details) return null;
  // A run that waits for a person says so: the Actions tab holds its
  // approvals and human tasks, and those of the runs that its steps called.
  const { waitingApprovalNodes, waitingHumanTaskNodes, waitingSubRunNodes } =
    getManualActionState(run.details);
  const actionsWaiting =
    waitingApprovalNodes.length +
      waitingHumanTaskNodes.length +
      waitingSubRunNodes.length >
    0;
  const showCopy = canWrite && stale;
  if (!canRunDags && !showCopy) return null;
  return (
    <div data-testid="ww-run-actions" className="flex items-center gap-1">
      {actionsWaiting && (
        <ActionButton
          label="wide"
          icon={<ClipboardCheck className="h-4 w-4" />}
          data-testid="ww-run-action-required"
          className="border-warning/60 text-warning hover:text-warning"
          onClick={() => dock.setTab('actions', { user: true })}
        >
          {ts('Action required')}
        </ActionButton>
      )}
      {canRunDags && (
        <span
          data-testid="ww-run-actions-slot"
          className="inline-flex min-w-7 shrink-0 items-center"
        >
          <DAGRunActions
            dagRun={run.details}
            name={run.ref.name}
            refresh={() => void run.refresh()}
            // The names show on a bar with room for them: an icon alone
            // reads as one of the copy and refresh icons beside it.
            displayMode="bar"
            hideUnavailable
            stopAll={stopAll}
            markActions
            // Like Start and Run step, a new run is shown at once; a removed
            // run must not stay in the URL as a broken link. A run under
            // another DAG name is not a run of this workflow.
            onRescheduled={(runId, runName) => {
              if (runName === dag.name) showRun(runId);
            }}
            onDequeued={() => run.select('latest', { replace: true })}
            onEditRetry={() => setEditRetry(true)}
          />
        </span>
      )}
      {canWrite && (canRunDags || showCopy) && (
        <span
          data-testid="ww-run-copy-slot"
          // The empty slot keeps the bar still on a wide window; a phone
          // needs the room for the name.
          className={cn(
            'inline-flex size-7 shrink-0 items-center justify-center',
            !showCopy && 'max-md:hidden'
          )}
        >
          {showCopy && (
            <IconButton
              label={ts('Copy to editor')}
              icon={FileInput}
              disabled={!copy.canCopy}
              onClick={copy.request}
            />
          )}
        </span>
      )}
      {canRunDags && (
        <EditRetryDialog
          open={editRetry}
          onClose={() => setEditRetry(false)}
          dagName={run.ref.name}
          dagRunId={run.ref.runId}
          fileName={fileName}
          // The new run shows at once, as after Start or Reschedule.
          onRetried={showRun}
        />
      )}
      {showCopy && (
        <ConfirmDialog
          title={ts('Replace the editor text?')}
          buttonText={ts('Replace')}
          visible={copy.confirming}
          dismissModal={copy.cancel}
          onSubmit={copy.confirm}
        >
          <p className="text-sm">
            {ts(
              'The editor has unsaved changes. The definition of this run replaces them.'
            )}
          </p>
        </ConfirmDialog>
      )}
    </div>
  );
}
