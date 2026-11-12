// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown } from 'lucide-react';
import { useMemo, type Ref } from 'react';
import { StatusDot } from '@/components/ui/status-dot';
import { useI18n } from '@/i18n/I18nProvider';
import { runDisplayStatus } from '@/lib/runDisplayStatus';
import { shortRunId } from '@/lib/shortRunId';
import { nodeStatusText, runStatusText } from '@/lib/status-utils';
import { cn, FOCUS_RING } from '@/lib/utils';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import type { ApiDAGDetails } from '../contracts';
import { useWorkspaceRun, type WorkspaceRunValue } from '../data';
import { useStepSubRunsOpener } from '../step/useStepRunActions';
import { useDockCommands } from './DockContext';
import { RunStepList } from './RunStepList';
import { stepReuse } from './stepReuse';
import { useDockStep } from './useDockStep';

/**
 * The steps of the selected run, as the vertical navigation on the left of
 * the Run panel. ?step is the selection, shared with the canvas. Without
 * ?step, the step that the auto-open chose is selected. A pick selects the
 * step on the canvas and opens its log; the summary row clears ?step. The
 * sub-run badge of a step opens its sub-runs. `onPicked` follows a pick.
 */
export function RunStepsNav({
  dag,
  onPicked,
}: {
  dag: ApiDAGDetails;
  onPicked?(): void;
}) {
  const { ts } = useI18n();
  const run = useWorkspaceRun();
  const { handle } = useWorkspaceEditor();
  const { openRunLog, openStep } = useDockCommands();
  const selected = useDockStep();
  const subRuns = useStepSubRunsOpener();

  if (!run.hasRun) return null;
  const details = run.details;
  if (!details) {
    return (
      <p className="p-3 text-xs text-muted-foreground">
        {ts('Loading run...')}
      </p>
    );
  }

  const select = (step: string | null) => {
    if (step === null) {
      openRunLog();
    } else {
      openStep(step, { keepTab: true });
      handle?.revealStep(step);
    }
    onPicked?.();
  };

  const openSubRun = (step: string, opts: { newTab: boolean }) => {
    const node = runNode(run, step);
    if (node) subRuns.open(node, details, opts);
  };

  return (
    <>
      <RunStepList
        dag={dag}
        details={details}
        handlers={run.handlers}
        selected={selected}
        onSelect={select}
        onOpenSubRun={openSubRun}
      />
      {subRuns.picker}
    </>
  );
}

/** The node of a step or a handler of the run. */
function runNode(
  run: Pick<WorkspaceRunValue, 'nodeByStep' | 'handlers'>,
  step: string
) {
  return (
    run.nodeByStep.get(step) ??
    run.handlers.find((handler) => handler.node.step.name === step)?.node
  );
}

/**
 * The selected row of the step list as one row, for a Run panel too narrow
 * for the list beside the tabs: the status and the name of the selected
 * step, or the run summary, with the dot of the list. It shows and hides
 * the list (`controls`).
 */
export function RunStepPicker({
  ref,
  open,
  controls,
  onToggle,
  className,
}: {
  ref?: Ref<HTMLButtonElement>;
  open: boolean;
  controls: string;
  onToggle(): void;
  className?: string;
}) {
  const { ts } = useI18n();
  const run = useWorkspaceRun();
  const selected = useDockStep();
  const details = run.details;
  const reuses = useMemo(
    () => (details ? stepReuse(details) : null),
    [details]
  );
  if (!run.hasRun || !details) return null;
  const node = selected === null ? undefined : runNode(run, selected);
  const reuse = selected === null ? undefined : reuses?.get(selected);
  const healthy = runDisplayStatus(details).healthy;
  // The dots are decorative: the button says the status once, after the name.
  // A step of the URL that the run does not have has no status of its own.
  const status = node
    ? reuse
      ? reuse.outcome === 'success'
        ? 'Reused, succeeded'
        : 'Reused, failed'
      : nodeStatusText(node.status)
    : selected !== null
      ? 'Not in this run'
      : healthy
        ? 'Healthy'
        : runStatusText(details.status);
  return (
    <button
      ref={ref}
      type="button"
      aria-expanded={open}
      aria-controls={controls}
      onClick={onToggle}
      className={cn(
        'h-8 w-full shrink-0 items-center gap-2 border-b border-border px-3 text-left text-xs font-medium text-foreground hover:bg-muted/60',
        FOCUS_RING,
        className
      )}
    >
      {node ? (
        <StatusDot
          kind="step"
          status={node.status}
          reused={reuse?.outcome}
          reusedFrom={reuse?.runId ? shortRunId(reuse.runId) : undefined}
          decorative
        />
      ) : selected === null ? (
        <StatusDot
          kind="run"
          status={details.status}
          healthy={healthy}
          decorative
          className="motion-safe:animate-none"
        />
      ) : null}
      <span className="min-w-0 flex-1 truncate">
        {selected ?? ts('Run summary')}
        <span className="sr-only">, {ts(status)}</span>
      </span>
      <ChevronDown
        aria-hidden="true"
        className={cn(
          'h-3.5 w-3.5 shrink-0 text-muted-foreground',
          open && 'rotate-180'
        )}
      />
    </button>
  );
}
