// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ClipboardCheck, FileClock } from 'lucide-react';
import { Status } from '@/api/v1/schema';
import { ActionButton } from '@/components/ui/action-button';
import { getManualActionState } from '@/features/dag-runs/lib/manualActionState';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { useRunSpecStale } from '@/features/visual-editor/run/useRunSpecStale';
import { useI18n } from '@/i18n/I18nProvider';
import type { BarSlotProps } from '../contracts';
import { useWorkspacePermissions } from '../data/useWorkspacePermissions';
import { useWorkspaceRun } from '../data/useWorkspaceRun';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { useDockCommands } from '../dock/DockContext';
import { useCopyRunSpecToEditor } from './useCopyRunSpecToEditor';
import { cn } from '@/lib/utils';

/**
 * The run notices of the selected run: the revert to the YAML of the run (with
 * write access, only while the run used a different definition than the saved
 * one), and the button that opens the Actions tab for a run that waits for a
 * person. The action of the run, such as Stop, Retry, Dequeue or Start, has
 * one slot on the right of the bar, in RunSplitButton. Absent for a workflow
 * that never ran.
 */
export function RunActionsCluster({ fileName, dag }: BarSlotProps) {
  const { ts } = useI18n();
  const { canRunDags, canWrite } = useWorkspacePermissions();
  const run = useWorkspaceRun();
  const [url, setUrl] = useWorkspaceUrl();
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
  // approvals and human tasks, those of the runs that its steps called, and
  // the retry of a resume that failed.
  const { waitingApprovalNodes, waitingHumanTaskNodes, waitingSubRunNodes } =
    getManualActionState(run.details);
  const resumeFailed =
    run.details.status === Status.Waiting &&
    (!!run.details.approvalResumePending ||
      !!run.details.humanTaskResumePending);
  const actionsWaiting =
    resumeFailed ||
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
      {canWrite && (canRunDags || showCopy) && (
        <span
          data-testid="ww-run-copy-slot"
          // The empty slot keeps the bar still on a wide window: there it has
          // the room of the named button. A phone needs the room for the name.
          className={cn(
            'inline-flex size-7 shrink-0 items-center justify-center @7xl:w-[11.5rem] @7xl:justify-start',
            !showCopy && 'max-md:hidden'
          )}
        >
          {showCopy && (
            // Named on a wide bar, as the run actions are.
            <ActionButton
              label="wide"
              className="max-w-full"
              icon={<FileClock className="h-4 w-4" />}
              title={ts(
                'Replaces the editor text with the YAML of this run. Nothing is saved until you save.'
              )}
              disabled={!copy.canCopy}
              onClick={copy.request}
            >
              {ts("Revert to this run's YAML")}
            </ActionButton>
          )}
        </span>
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
