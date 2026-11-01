// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AlertTriangle, ChevronDown, FilePen, Play } from 'lucide-react';
import { useEffect, useState } from 'react';
import { flushSync } from 'react-dom';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/spinner';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import DAGActions from '@/features/dags/components/common/DAGActions';
import { useEditorRunActions } from '@/features/visual-editor/run/useEditorRunActions';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import type { BarSlotProps } from '../contracts';
import { useWorkspaceData } from '../data/context';
import { useWorkspacePermissions } from '../data/useWorkspacePermissions';
import { useWorkspaceRefresh } from '../data/useWorkspaceRefresh';
import { Z } from '../zLayers';
import { RunStartTarget } from './RunStartContext';
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
 * caret has its slot whenever the user may run a draft: it is disabled, not
 * removed, while the editor equals the saved file, so that Start keeps its
 * place when the draft becomes dirty.
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
  // The buffer is read when the draft runs, not on every render.
  const [spec, setSpec] = useState('');
  const actions = useEditorRunActions({
    fileName,
    dagName: dag.name,
    spec,
    isDirty: dirty,
  });
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
  const hasMenu = canExecute && (dirty || canRunDags);
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
            <DAGActions
              only={['start']}
              displayMode="full"
              startIconOnly
              // While the slot shows busy or error, the feedback icon takes the
              // place of the Play icon: the button keeps its size.
              startIconHidden={busy || Boolean(shownError)}
              status={dagData.latestDAGRun}
              dag={dag}
              fileName={fileName}
              refresh={() => void refresh()}
              markActions
            />
          </RunStartTarget>
          {(busy || shownError) && (
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
      {hasMenu && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              size={canRunDags ? 'icon-sm' : 'sm'}
              disabled={!dirty}
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
            <DropdownMenuItem
              disabled={busy}
              onSelect={runDraft}
              className="gap-2"
            >
              <FilePen aria-hidden="true" className="size-4 shrink-0" />
              {ts('Run unsaved draft')}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  );
}
