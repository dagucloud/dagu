// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { X } from 'lucide-react';
import { lazy, Suspense, useMemo, useState, type ReactElement } from 'react';
import type { components } from '@/api/v1/schema';
import { IconButton } from '@/components/ui/icon-button';
import StatusChip from '@/components/ui/status-chip';
import { StatusDot } from '@/components/ui/status-dot';
import { RunMetaLine } from '@/features/dag-runs/components/common/RunMetaLine';
import {
  RunError,
  RunNodeProgress,
  RunStatusMessage,
} from '@/features/dags/components/common/RunSummaryBlocks';
import StepLog from '@/features/dags/components/dag-execution/StepLog';
import { buildRunOverlay } from '@/features/visual-editor/run/runOverlay';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { ApiNode } from '../contracts';
import {
  allSubRunsOf,
  renderParallelPicker,
  useSubRunOpener,
} from '../step/useStepRunActions';
import { Z } from '../zLayers';

const DetailsCanvas = lazy(
  () => import('@/features/visual-editor/DetailsCanvas')
);

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type DAGDetails = components['schemas']['DAGDetails'];

/** The definition that a run ran: its steps and its handlers. */
function dagOfRun(run: DAGRunDetails): DAGDetails {
  return {
    name: run.name,
    steps: run.nodes.map((node) => node.step),
    handlerOn: {
      init: run.onInit?.step,
      success: run.onSuccess?.step,
      failure: run.onFailure?.step,
      abort: run.onAbort?.step,
      exit: run.onExit?.step,
      wait: run.onWait?.step,
    },
  } as DAGDetails;
}

/**
 * A child run on the sub-run stack, in the form of the workflow page: the
 * facts of the run, its graph with the state of each step, and the log of
 * the picked step. Opening a step that has child runs (a double click or
 * Enter) opens them on the same stack.
 */
export function SubRunView({
  run,
  initialStep,
}: {
  run: DAGRunDetails;
  /** A step to show when the run opens. */
  initialStep?: string;
}): ReactElement {
  const dag = useMemo(() => dagOfRun(run), [run]);
  const overlay = useMemo(() => buildRunOverlay(run), [run]);
  const [picked, setPicked] = useState<string | null>(initialStep ?? null);
  const [picker, setPicker] = useState<ApiNode | null>(null);
  const openSubRun = useSubRunOpener();
  const nodeOf = (step: string) =>
    run.nodes.find((node) => node.step.name === step);

  const openStep = (step: string) => {
    const node = nodeOf(step);
    const children = allSubRunsOf(node).length;
    if (node && children === 1) openSubRun({ node, index: 0, holder: run });
    else if (node && children > 1) setPicker(node);
    else setPicked(step);
  };

  const pickedNode = picked === null ? undefined : nodeOf(picked);

  return (
    <div className="flex h-full min-h-0 flex-col gap-3">
      <section className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <StatusChip status={run.status} size="sm">
            {run.statusLabel}
          </StatusChip>
          <RunStatusMessage status={run.status} />
        </div>
        <RunMetaLine dagRun={run} />
        <RunNodeProgress nodes={run.nodes} />
        <RunError error={run.error} />
      </section>
      <div className="relative min-h-[360px] flex-1">
        <Suspense fallback={<div className="h-full rounded-md bg-muted/30" />}>
          <DetailsCanvas
            dag={dag}
            overlay={overlay}
            className="h-full"
            onSelectStep={setPicked}
            onOpenStep={openStep}
          />
        </Suspense>
        {picked !== null && pickedNode && (
          <RunStepPane
            run={run}
            node={pickedNode}
            onClose={() => setPicked(null)}
          />
        )}
      </div>
      {renderParallelPicker(
        picker ? { node: picker, holder: run } : null,
        () => setPicker(null),
        openSubRun
      )}
    </div>
  );
}

/** The state and the log of one step of the run, over the right of the graph. */
function RunStepPane({
  run,
  node,
  onClose,
}: {
  run: DAGRunDetails;
  node: ApiNode;
  onClose(): void;
}): ReactElement {
  const { ts } = useI18n();
  const step = node.step.name;
  return (
    <aside
      aria-label={ts('Details of {step}', { step })}
      className={cn(
        'absolute inset-y-0 right-0 flex w-[min(32rem,60%)] flex-col rounded-r-md border-l border-border bg-card shadow-lg shadow-foreground/10',
        Z.canvasOverlay
      )}
    >
      <header className="flex h-10 shrink-0 items-center gap-2 border-b border-border px-3">
        <StatusDot
          kind="step"
          status={node.status}
          decorative
          className="h-2 w-2 shrink-0"
        />
        <div className="min-w-0 flex-1 leading-tight">
          <h3 className="truncate text-sm font-semibold text-foreground">
            {step}
          </h3>
          <p className="truncate text-[11px] text-muted-foreground">
            {node.statusLabel}
          </p>
        </div>
        <IconButton icon={X} size="sm" label={ts('Close')} onClick={onClose} />
      </header>
      <div className="min-h-0 flex-1 overflow-auto">
        <StepLog
          // The viewer keeps the stream and the page of one step only.
          key={step}
          dagName={run.name}
          dagRunId={run.dagRunId}
          stepName={step}
          dagRun={run}
          node={node}
        />
      </div>
    </aside>
  );
}
