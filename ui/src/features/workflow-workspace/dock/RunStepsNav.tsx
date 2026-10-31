// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useI18n } from '@/i18n/I18nProvider';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import type { ApiDAGDetails } from '../contracts';
import { useWorkspaceRun } from '../data';
import { useDockCommands } from './DockContext';
import { RunStepList } from './RunStepList';
import { useDockStep } from './useDockStep';

/**
 * The steps of the selected run, as the vertical navigation on the left of
 * the Run panel. ?step is the selection, shared with the canvas. Without
 * ?step, the step that the auto-open chose is selected. A pick selects the
 * step on the canvas and opens its log; the summary row clears ?step.
 */
export function RunStepsNav({ dag }: { dag: ApiDAGDetails }) {
  const { ts } = useI18n();
  const run = useWorkspaceRun();
  const { handle } = useWorkspaceEditor();
  const { openRunLog, openStep } = useDockCommands();
  const selected = useDockStep();

  if (!run.hasRun) return null;
  const details = run.details;
  if (!details) {
    return (
      <p className="p-3 text-xs text-muted-foreground @max-[900px]:sr-only">
        {ts('Loading run...')}
      </p>
    );
  }

  const select = (step: string | null) => {
    if (step === null) {
      openRunLog();
      return;
    }
    openStep(step, { keepTab: true });
    handle?.revealStep(step);
  };

  return (
    <RunStepList
      dag={dag}
      details={details}
      handlers={run.handlers}
      selected={selected}
      onSelect={select}
    />
  );
}
