// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { FileInput } from 'lucide-react';
import { useMemo } from 'react';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { IconButton } from '@/components/ui/icon-button';
import DAGRunActions from '@/features/dag-runs/components/common/DAGRunActions';
import { useRunSpecStale } from '@/features/visual-editor/run/useRunSpecStale';
import { useI18n } from '@/i18n/I18nProvider';
import type { BarSlotProps } from '../contracts';
import { useWorkspacePermissions } from '../data/useWorkspacePermissions';
import { useWorkspaceRun } from '../data/useWorkspaceRun';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { useCopyRunSpecToEditor } from './useCopyRunSpecToEditor';
import { useShowRun } from './useShowRun';

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
  const showCopy = canWrite && stale;
  if (!canRunDags && !showCopy) return null;
  return (
    <div data-testid="ww-run-actions" className="flex items-center gap-1">
      {canRunDags && (
        <span
          data-testid="ww-run-actions-slot"
          className="inline-flex min-w-7 shrink-0 items-center"
        >
          <DAGRunActions
            dagRun={run.details}
            name={run.ref.name}
            refresh={() => void run.refresh()}
            displayMode="compact"
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
          />
        </span>
      )}
      {canWrite && (canRunDags || showCopy) && (
        <span
          data-testid="ww-run-copy-slot"
          className="inline-flex size-7 shrink-0 items-center justify-center"
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
