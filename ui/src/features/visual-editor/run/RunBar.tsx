// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/spinner';
import { useCanExecuteForWorkspace } from '@/contexts/AuthContext';
import { useI18n } from '@/i18n/I18nProvider';
import type { EditorRunActions } from '../contracts';
import { useRunSpecStale } from './useRunSpecStale';

export function RunBar(p: {
  dagName: string;
  fileName: string;
  runId: string | null;
  onRunIdChange(id: string | null): void;
  actions: EditorRunActions;
  isDirty: boolean;
  /** Workspace of the DAG: the run permission is checked there. */
  workspace?: string | null;
}): ReactElement {
  const { ts } = useI18n();
  const canExecute = useCanExecuteForWorkspace(p.workspace);
  const stale = useRunSpecStale(p);

  const { actions } = p;
  const busy = actions.isStarting;
  const noPermission = ts('You do not have permission to run workflows.');
  // The saved definition starts from the Start button of the header, which
  // opens the start dialog. Only the draft has no other way to run.

  // Starting a run shows it, so the overlay colors the canvas with the new run.
  const start = async (run: () => Promise<string | null>) => {
    const id = await run();
    if (id !== null) p.onRunIdChange(id);
  };

  return (
    <div className="flex flex-wrap items-center gap-2 text-xs">
      {p.isDirty && (
        <Button
          size="xs"
          disabled={!canExecute || busy}
          title={!canExecute ? noPermission : undefined}
          onClick={() => void start(actions.runDraft)}
        >
          {ts('Run unsaved draft')}
        </Button>
      )}
      {stale && (
        <span className="text-muted-foreground">
          {ts('This run used a different definition than the saved one.')}
        </span>
      )}
      {actions.error && (
        <span role="alert" className="min-w-0 break-words text-destructive">
          {actions.error}
        </span>
      )}
      {busy && (
        <Spinner label={ts('Starting run')} className="text-muted-foreground" />
      )}
    </div>
  );
}
