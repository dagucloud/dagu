// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Archive,
  ChevronDown,
  ChevronUp,
  FileCode,
  GanttChart,
  Info,
  ScrollText,
  X,
} from 'lucide-react';
import {
  lazy,
  Suspense,
  useContext,
  useId,
  useMemo,
  useState,
  type ReactElement,
} from 'react';
import { NodeStatus, Stream, type components } from '@/api/v1/schema';
import { IconButton } from '@/components/ui/icon-button';
import StatusChip from '@/components/ui/status-chip';
import { StatusDot } from '@/components/ui/status-dot';
import { TabStrip, tabStripTabId } from '@/components/ui/tab-strip';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { RunMetaLine } from '@/features/dag-runs/components/common/RunMetaLine';
import {
  RunError,
  RunNodeProgress,
  RunStatusMessage,
} from '@/features/dags/components/common/RunSummaryBlocks';
import ArtifactsTab from '@/features/dags/components/artifacts/ArtifactsTab';
import ExecutionLog from '@/features/dags/components/dag-execution/ExecutionLog';
import StepLog from '@/features/dags/components/dag-execution/StepLog';
import TimelineChart from '@/features/dags/components/visualization/TimelineChart';
import { buildRunOverlay } from '@/features/visual-editor/run/runOverlay';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { ApiNode } from '../contracts';
import { WorkspaceDataContext } from '../data/context';
import { StepFacts } from '../step/StepFacts';
import {
  LogPageLink,
  runLogPageURL,
  stepLogPageURL,
} from '../step/StepLogPane';
import { StepManualActions } from '../step/StepManualActions';
import type { StepRunTarget } from '../step/StepRunView';
import {
  allSubRunsOf,
  renderParallelPicker,
  useSubRunOpener,
} from '../step/useStepRunActions';
import { Z } from '../zLayers';

const DetailsCanvas = lazy(
  () => import('@/features/visual-editor/DetailsCanvas')
);
const DAGSpecReadOnly = lazy(
  () => import('@/features/dags/components/dag-editor/DAGSpecReadOnly')
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
 * facts of the run, its graph with the state of each step, and the facts,
 * actions and log of the picked step. Opening a step that has child runs (a double click or
 * Enter) opens them on the same stack.
 */
export function SubRunView({
  run,
  initialStep,
  onChanged,
}: {
  run: DAGRunDetails;
  /** A step to show when the run opens. */
  initialStep?: string;
  /** Called after a step action changed the run: the host reads it again. */
  onChanged?(): void;
}): ReactElement {
  const dag = useMemo(() => dagOfRun(run), [run]);
  const overlay = useMemo(() => buildRunOverlay(run), [run]);
  // A run that waits for a person opens on the step that waits.
  const [picked, setPicked] = useState<string | null>(
    () =>
      initialStep ??
      run.nodes.find((node) => node.status === NodeStatus.Waiting)?.step.name ??
      null
  );
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
      <div className="relative min-h-[240px] flex-1">
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
            // Each step opens on its own tab and stream.
            key={picked}
            run={run}
            node={pickedNode}
            onClose={() => setPicked(null)}
            onChanged={onChanged}
          />
        )}
      </div>
      <SubRunActivity run={run} />
      {renderParallelPicker(
        picker ? { node: picker, holder: run } : null,
        () => setPicker(null),
        openSubRun
      )}
    </div>
  );
}

/** The workflow file of the page, which the log pages of a child run name. */
function usePageFile(run: DAGRunDetails): string {
  return useContext(WorkspaceDataContext)?.fileName ?? run.name;
}

type ActivityTab = 'timeline' | 'log' | 'artifacts' | 'definition';

/**
 * The record of the run under its graph, as the Activity panel of the
 * workflow page: the timeline of its steps, its run log, its artifacts and
 * the definition that it ran. A child row of the timeline opens on the
 * same stack. The panel folds to its tab row.
 */
function SubRunActivity({ run }: { run: DAGRunDetails }): ReactElement {
  const { ts } = useI18n();
  const fileName = usePageFile(run);
  const remoteNode = useRemoteNode();
  const idPrefix = useId();
  const [tab, setTab] = useState<ActivityTab>('timeline');
  const [open, setOpen] = useState(true);
  return (
    <section
      aria-label={ts('Activity')}
      className={cn(
        'flex shrink-0 flex-col rounded-md border border-border bg-card',
        open && 'h-[40%] min-h-[200px]'
      )}
    >
      <div className="flex shrink-0 items-center gap-1 border-b border-border pr-1">
        <TabStrip
          label={ts('Activity')}
          idPrefix={idPrefix}
          className="min-w-0 flex-1 px-2"
          tabs={[
            { id: 'timeline', label: ts('Timeline'), icon: GanttChart },
            { id: 'log', label: ts('Run log'), icon: ScrollText },
            { id: 'artifacts', label: ts('Artifacts'), icon: Archive },
            { id: 'definition', label: ts('Definition'), icon: FileCode },
          ]}
          value={tab}
          onChange={(id) => {
            setTab(id as ActivityTab);
            setOpen(true);
          }}
        />
        <IconButton
          icon={open ? ChevronDown : ChevronUp}
          size="sm"
          label={ts(open ? 'Hide activity' : 'Show activity')}
          onClick={() => setOpen(!open)}
        />
      </div>
      {open && (
        <div
          role="tabpanel"
          aria-labelledby={tabStripTabId(idPrefix, tab)}
          className="min-h-0 flex-1 overflow-auto"
        >
          {tab === 'timeline' && (
            <div className="p-2">
              <TimelineChart status={run} />
            </div>
          )}
          {tab === 'log' && (
            <div className="h-full min-h-0 p-2">
              <ExecutionLog
                name={run.name}
                dagRunId={run.dagRunId}
                dagRun={run}
                toolbarExtra={
                  <LogPageLink
                    to={runLogPageURL({ fileName, holder: run, remoteNode })}
                  />
                }
              />
            </div>
          )}
          {tab === 'artifacts' && (
            <ArtifactsTab dagRun={run} className="h-full min-h-0" fillHeight />
          )}
          {tab === 'definition' && (
            <Suspense fallback={null}>
              <DAGSpecReadOnly
                dagName={run.rootDAGRunName || run.name}
                dagRunId={run.rootDAGRunId || run.dagRunId}
                subDAGRunId={run.dagRunId}
                className="p-2"
              />
            </Suspense>
          )}
        </div>
      )}
    </section>
  );
}

type PaneTab = 'details' | 'log';

/**
 * One step of the run, over the right of the graph: its facts and actions
 * (retry, status, approval) in Details, its output in Log. A step that waits
 * for a person opens on Details, any other on Log.
 */
function RunStepPane({
  run,
  node,
  onClose,
  onChanged,
}: {
  run: DAGRunDetails;
  node: ApiNode;
  onClose(): void;
  onChanged?(): void;
}): ReactElement {
  const { ts } = useI18n();
  const step = node.step.name;
  const fileName = usePageFile(run);
  const remoteNode = useRemoteNode();
  const idPrefix = useId();
  const [tab, setTab] = useState<PaneTab>(
    node.status === NodeStatus.Waiting ? 'details' : 'log'
  );
  const [stream, setStream] = useState<Stream>(Stream.stdout);
  const target = useMemo<StepRunTarget>(
    () => ({ step, subRun: { dagName: run.name, subDAGRunId: run.dagRunId } }),
    [run.dagRunId, run.name, step]
  );
  const openLogs = (t: StepRunTarget) => {
    setStream(t.stream === 'stderr' ? Stream.stderr : Stream.stdout);
    setTab('log');
  };
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
      <TabStrip
        label={ts('Step')}
        idPrefix={idPrefix}
        className="shrink-0 border-b border-border px-2"
        tabs={[
          { id: 'details', label: ts('Details'), icon: Info },
          { id: 'log', label: ts('Log'), icon: ScrollText },
        ]}
        value={tab}
        onChange={(id) => setTab(id as PaneTab)}
      />
      <div
        role="tabpanel"
        aria-labelledby={tabStripTabId(idPrefix, tab)}
        className="min-h-0 flex-1 overflow-auto"
      >
        {tab === 'details' ? (
          <div className="space-y-3 p-3">
            <StepFacts
              target={target}
              holder={run}
              node={node}
              onOpenLogs={openLogs}
              onRunChanged={onChanged}
            />
            <StepManualActions holder={run} node={node} inSubRun />
          </div>
        ) : (
          <StepLog
            dagName={run.name}
            dagRunId={run.dagRunId}
            stepName={step}
            dagRun={run}
            node={node}
            stream={stream}
            toolbarExtra={
              <LogPageLink
                to={stepLogPageURL({
                  fileName,
                  holder: run,
                  remoteNode,
                  step,
                  stream,
                })}
              />
            }
          />
        )}
      </div>
    </aside>
  );
}
