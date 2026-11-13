// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AlertTriangle, ChevronDown, FilePen, Play, Save } from 'lucide-react';
import { Status } from '@/api/v1/schema';
import { useEffect, useMemo, useState } from 'react';
import { flushSync } from 'react-dom';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/spinner';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import DAGRunActions from '@/features/dag-runs/components/common/DAGRunActions';
import { getDAGRunTerminateAction } from '@/features/dag-runs/components/common/terminateAction';
import { getManualActionState } from '@/features/dag-runs/lib/manualActionState';
import { useUnsavedChanges } from '@/contexts/UnsavedChangesContext';
import DAGActions from '@/features/dags/components/common/DAGActions';
import { useEditorRunActions } from '@/features/visual-editor/run/useEditorRunActions';
import { useI18n } from '@/i18n/I18nProvider';
import { hasActiveServiceRun, restartsByItself } from '@/lib/dagSchedule';
import { cn } from '@/lib/utils';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import type { BarSlotProps } from '../contracts';
import { useWorkspaceData } from '../data/context';
import { useServiceState } from '../data/useServiceDag';
import { useWorkspaceRun } from '../data/useWorkspaceRun';
import { useWorkspacePermissions } from '../data/useWorkspacePermissions';
import { useWorkspaceRefresh } from '../data/useWorkspaceRefresh';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { Z } from '../zLayers';
import { EditRetryDialog } from './EditRetryDialog';
import { RunStartTarget, useRunStart } from './RunStartContext';
import { useShowRun } from './useShowRun';

// How long the failed-start icon replaces the Play icon.
const ERROR_SHOWN_MS = 5000;

/** The error of the last failed start for 5 s, then null. */
function useShownError(error: string | null, busy: boolean): string | null {
  const [shown, setShown] = useState<string | null>(null);
  useEffect(() => {
    if (!error || busy) {
      setShown(null);
      return;
    }
    setShown(error);
    const timer = window.setTimeout(() => setShown(null), ERROR_SHOWN_MS);
    return () => window.clearTimeout(timer);
  }, [error, busy]);
  return shown;
}

/** Spinner while the start is pending, a destructive alert after it failed. */
function StartFeedback({
  busy,
  error,
  label,
}: {
  busy: boolean;
  error: string | null;
  label: string;
}) {
  if (busy) {
    return <Spinner label={label} className="size-4 text-primary-foreground" />;
  }
  return (
    <span role="alert" className="grid place-items-center">
      <AlertTriangle aria-hidden="true" className="size-4 text-destructive" />
      <span className="sr-only">{error}</span>
    </span>
  );
}

/**
 * The Start icon button and a caret menu with the unsaved draft. The primary
 * button is the only DAGActions of the workspace, start only: it keeps the
 * unsaved guard and the Start dialog, which runs the saved definition. The
 * caret has its slot whenever the user may run a draft, so that Start keeps
 * its place when the draft becomes dirty. While the editor equals the saved
 * file, its menu says that there is no draft to run. With unsaved changes,
 * the menu also offers to save and run to a user who may do both.
 */
export function RunSplitButton(props: BarSlotProps) {
  const { canRunDags, canExecute } = useWorkspacePermissions();
  if (!canRunDags && !canExecute) return null;
  return (
    <SplitButton {...props} canRunDags={canRunDags} canExecute={canExecute} />
  );
}

function SplitButton({
  fileName,
  dag,
  canRunDags,
  canExecute,
}: BarSlotProps & { canRunDags: boolean; canExecute: boolean }) {
  const { ts } = useI18n();
  const { dagData } = useWorkspaceData('RunSplitButton');
  const showRun = useShowRun();
  const refresh = useWorkspaceRefresh();
  const { handle, state } = useWorkspaceEditor();
  const dirty = state?.dirty ?? false;
  const { hasUnsavedChanges } = useUnsavedChanges();
  // The buffer is read when the draft runs, not on every render.
  const [spec, setSpec] = useState('');
  const actions = useEditorRunActions({
    fileName,
    dagName: dag.name,
    spec,
    isDirty: dirty,
  });
  const serviceState = useServiceState(fileName);
  const run = useWorkspaceRun();
  // Each state of the shown run has one action in the slot of Start: Dequeue
  // for a queued run; Stop (or Reject, for a run that waits for an approval)
  // for an active run; Retry for a failed run; else Start.
  const { isWaiting, waitingApprovalNodes } = getManualActionState(
    run.details ?? undefined
  );
  const dequeueShown = !!run.ref && run.details?.status === Status.Queued;
  const stopShown =
    !dequeueShown &&
    !!run.ref &&
    !!run.details &&
    (getDAGRunTerminateAction(run.details) !== 'none' ||
      (isWaiting && waitingApprovalNodes.length > 0));
  // A run that failed has Retry in the slot of Start. A service workflow
  // that has an active run, or that the scheduler restarts by itself, keeps
  // the button disabled: it has one run at a time.
  const retryShown =
    !stopShown &&
    !dequeueShown &&
    !!run.ref &&
    run.details?.status === Status.Failed;
  const replaced = stopShown || retryShown || dequeueShown;
  const [url] = useWorkspaceUrl();
  const [editRetry, setEditRetry] = useState(false);
  const startFlow = useRunStart();
  const { canSaveAndStart, requestSaveAndStart } = startFlow;
  const stopAll = useMemo(() => ({ fileName }), [fileName]);
  const busy = actions.isStarting;
  const shownError = useShownError(actions.error, busy);

  const start = async (runIt: () => Promise<string | null>) => {
    const id = await runIt();
    if (id === null) return;
    void refresh();
    showRun(id);
  };

  const runDraft = () => {
    const buffer = handle?.getBuffer() ?? '';
    // The hook reads its arguments after a commit, so commit the buffer first.
    flushSync(() => setSpec(buffer));
    void start(actions.runDraft);
  };

  // The draft is the one choice that Start does not give. With Start beside
  // it, the caret keeps its slot while there is no draft; alone, it shows
  // only for a draft.
  // A service workflow with an active run refuses a draft run, so its menu
  // has nothing to offer. So does one that the scheduler restarts by itself:
  // it starts the next run, and Start is not shown.
  const serviceRestarts = restartsByItself(serviceState);
  const serviceBusy =
    hasActiveServiceRun(dag, dagData.latestDAGRun) || serviceRestarts;
  const canRunDraft = canExecute && !serviceBusy && (dirty || canRunDags);
  const canSaveAndRun = canSaveAndStart && hasUnsavedChanges && !serviceBusy;
  const hasMenu = canRunDraft || canSaveAndRun;
  if (!canRunDags && !hasMenu) return null;

  return (
    <div data-testid="ww-run-split" className="flex min-w-0 items-center gap-1">
      {canRunDags && (
        <div
          data-testid="ww-run-start-slot"
          className="relative"
          title={shownError ?? undefined}
        >
          <RunStartTarget>
            {replaced && run.ref && run.details && (
              <DAGRunActions
                part={dequeueShown ? 'dequeue' : stopShown ? 'stop' : 'retry'}
                dagRun={run.details}
                name={run.ref.name}
                refresh={() => void run.refresh()}
                displayMode="bar"
                hideUnavailable
                stopAll={stopAll}
                service={{
                  dag,
                  latestRun: dagData.latestDAGRun,
                  state: serviceState,
                }}
                markActions
                // A new run is shown at once. A run under another DAG name is
                // not a run of this workflow.
                onRescheduled={(runId, runName) => {
                  if (runName === dag.name) showRun(runId, url.runName);
                }}
                onEditRetry={() => setEditRetry(true)}
              />
            )}
            {/* Start stays mounted, hidden: the palette and the menu open its
                dialog while Stop or Retry has the slot. */}
            <div className={replaced ? 'hidden' : 'contents'}>
              <DAGActions
                only={['start']}
                displayMode="full"
                barStart
                // While the slot shows busy or error, the feedback icon takes the
                // place of the Play icon: the button keeps its size.
                startIconHidden={busy || Boolean(shownError)}
                status={dagData.latestDAGRun}
                dag={dag}
                fileName={fileName}
                refresh={() => void refresh()}
                markActions
                serviceState={serviceState}
              />
            </div>
          </RunStartTarget>
          {(busy || shownError) && !replaced && (
            <span
              className={cn(
                'pointer-events-none absolute inset-0 grid place-items-center',
                // The alert icon needs a surface that it reads on.
                !busy &&
                  'rounded-md bg-background ring-1 ring-inset ring-destructive/50'
              )}
            >
              <StartFeedback
                busy={busy}
                error={shownError}
                label={ts('Starting run')}
              />
            </span>
          )}
        </div>
      )}
      {canRunDags && run.ref && (
        <EditRetryDialog
          open={editRetry}
          onClose={() => setEditRetry(false)}
          dagName={run.ref.name}
          dagRunId={run.ref.runId}
          fileName={fileName}
          // The new run shows at once, as after Start or Reschedule. It keeps
          // the DAG name of the retried run.
          onRetried={(runId) => showRun(runId, url.runName)}
        />
      )}
      {hasMenu && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              size={canRunDags ? 'icon-sm' : 'sm'}
              aria-label={ts('More run options')}
              title={ts('More run options')}
            >
              {!canRunDags &&
                (busy || shownError ? (
                  <StartFeedback
                    busy={busy}
                    error={shownError}
                    label={ts('Starting run')}
                  />
                ) : (
                  <Play aria-hidden="true" />
                ))}
              {!canRunDags && ts('Run')}
              <ChevronDown aria-hidden="true" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className={cn(Z.menu, 'w-64')}>
            {retryShown && canRunDags && (
              <DropdownMenuItem
                onSelect={() => startFlow.requestStart()}
                className="gap-2"
              >
                <Play aria-hidden="true" className="size-4 shrink-0" />
                {ts('Start')}
              </DropdownMenuItem>
            )}
            {canSaveAndRun && (
              <DropdownMenuItem
                disabled={busy}
                onSelect={requestSaveAndStart}
                className="gap-2"
              >
                <Save aria-hidden="true" className="size-4 shrink-0" />
                {ts('Save & Run')}
              </DropdownMenuItem>
            )}
            {canRunDraft && (
              <DropdownMenuItem
                disabled={busy || !dirty}
                onSelect={runDraft}
                className="gap-2"
              >
                <FilePen aria-hidden="true" className="size-4 shrink-0" />
                <span className="flex min-w-0 flex-col">
                  {ts('Run unsaved draft')}
                  {!dirty && (
                    <span className="text-xs text-muted-foreground">
                      {ts('No unsaved changes')}
                    </span>
                  )}
                </span>
              </DropdownMenuItem>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  );
}
