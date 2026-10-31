// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  createElement,
  Fragment,
  useCallback,
  useEffect,
  useState,
  type ReactNode,
} from 'react';
import { useNavigate } from 'react-router-dom';
import type { components, NodeStatus } from '@/api/v1/schema';
import { useErrorModal } from '@/components/ui/error-modal';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { StepRetryDialog } from '@/features/dags/components/common/StepRetryDialog';
import { useOpenSubRun } from '@/features/dags/components/common/SubRunStackModal';
import { ParallelExecutionModal } from '@/features/dags/components/dag-execution/ParallelExecutionModal';
import StatusUpdateModal from '@/features/dags/components/dag-execution/StatusUpdateModal';
import { isSubDAGRun } from '@/features/dags/hooks/useStepLogQuery';
import {
  canChangeStepStatus,
  canRetryStep,
} from '@/features/dags/lib/stepStatusGate';
import type { FollowedStepRun } from '@/features/visual-editor/contracts';
import { useEditorRunActions } from '@/features/visual-editor/run/useEditorRunActions';
import { parseDagDoc, resolveStepNames } from '@/features/visual-editor/doc';
import { referencedStepNames } from '@/features/visual-editor/references/stepReferences';
import {
  stepsWithUpstream,
  upstreamStepNames,
} from '@/features/visual-editor/run/upstreamSteps';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import type { ApiDAGDetails, ApiDAGRunDetails, ApiNode } from '../contracts';
import { useWorkspaceData } from '../data/context';
import { useWorkspacePermissions } from '../data/useWorkspacePermissions';
import { useDockCommands } from '../dock/DockContext';
import { noteTestRunStarted } from '../data/useRunHistory';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import {
  StepRunConfirmDialog,
  StepRunReuseDialog,
  type StepRunPick,
} from './StepRunConfirmDialog';
import type { StepRunTarget } from './StepRunView';

export interface StepRunGates {
  retry: boolean;
  setStatus: boolean;
  runStep: boolean;
  subRuns: boolean;
  reason?: string;
}

export interface StepRunActions {
  can(step: string): StepRunGates;
  /** StepRetryDialog */
  retryStep(step: string): void;
  /** StatusUpdateModal -> PATCH -> run.applyNodeStatus -> run.refresh */
  setStatus(step: string): void;
  /**
   * Confirm, then run the step. In the dialog, Run starts the previous steps
   * (the upstream steps and the steps that the step reads from) and the
   * step, on no earlier output. Step only starts the step alone, and Changed
   * steps starts the previous steps that changed or hold no output, then the
   * step; both copy the outputs of the reuse source (`useReuseSource`). A step
   * with no previous step has only Run.
   */
  runStep(step: string): void;
  /** Confirm, then run the upstream steps of the step, not the step itself. */
  runUpstream(step: string): void;
  /**
   * With `stay`, the runs of a step whose view is open (?step and ?ndv) start
   * without a page change and show here; they are the ones that the step view
   * follows. The list keeps the last run of each kind. A run of the dialog
   * with previous steps gives an 'upstream' and an 'execute' entry.
   */
  followed: readonly FollowedStepRun[];
  clearFollowed(): void;
  openSubRun(step: string, e?: { newTab?: boolean }): void;
  /** dock.openStep(t) */
  openLogs(t: StepRunTarget): void;
  dialogs: ReactNode;
}

type SubRun = components['schemas']['SubDAGRun'];

/** The child runs of a step: the parallel ones, then the repeated ones. */
export function allSubRunsOf(node: ApiNode | undefined): SubRun[] {
  if (!node) return [];
  return [...(node.subRuns ?? []), ...(node.subRunsRepeated ?? [])];
}

/** The name that labels the child runs of a step. */
export function subDagNameOf(node: ApiNode): string {
  return node.step.call || allSubRunsOf(node)[0]?.dagName || '';
}

export interface OpenSubRunArgs {
  node: ApiNode;
  index: number;
  /** The run that holds the step. */
  holder: ApiDAGRunDetails;
  newTab?: boolean;
}

/**
 * Opens one child run of a step. A plain click pushes it on the sub-run
 * stack; a new tab, or a view outside a stack, opens the run page.
 */
export function useSubRunOpener(): (args: OpenSubRunArgs) => void {
  const openOnStack = useOpenSubRun();
  const remoteNode = useRemoteNode();
  const navigate = useNavigate();
  return useCallback(
    ({ node, index, holder, newTab }: OpenSubRunArgs) => {
      const child = allSubRunsOf(node)[index];
      if (!child?.dagRunId) return;
      if (openOnStack && !newTab) {
        openOnStack({
          name: child.dagName || node.step.call || node.step.name,
          dagRunId: child.dagRunId,
        });
        return;
      }
      const url = buildDAGRunPageURL({
        rootDAGRunName: holder.rootDAGRunName || holder.name,
        rootDAGRunId: holder.rootDAGRunId || holder.dagRunId,
        subDAGRunId: child.dagRunId,
        remoteNode,
      });
      if (newTab) {
        window.open(url, '_blank');
      } else {
        navigate(url);
      }
    },
    [navigate, openOnStack, remoteNode]
  );
}

/** ParallelExecutionModal for the child runs of one step; a pick opens it with useSubRunOpener. */
export function renderParallelPicker(
  picker: { node: ApiNode; holder: ApiDAGRunDetails } | null,
  close: () => void,
  open: (args: OpenSubRunArgs) => void
): ReactNode {
  if (!picker) return null;
  const { node, holder } = picker;
  return createElement(ParallelExecutionModal, {
    isOpen: true,
    onClose: close,
    stepName: node.step.name,
    subDAGName: subDagNameOf(node),
    subRuns: allSubRunsOf(node),
    rootDagName: holder.rootDAGRunName || holder.name,
    rootDagRunId: holder.rootDAGRunId || holder.dagRunId,
    parentDagRunId: holder.dagRunId,
    onSelectSubRun: (index: number, newTab?: boolean) => {
      open({ node, index, holder, newTab });
      if (!newTab) close();
    },
  });
}

/**
 * The run actions on one step of the selected run, with their gates and
 * the dialogs that they open. Render `dialogs` where the hook is called.
 */
export function useStepRunActions(
  opts: { stay?: boolean } = {}
): StepRunActions {
  const { stay = false } = opts;
  const { ts } = useI18n();
  const { fileName, dagData, run, refresh } =
    useWorkspaceData('useStepRunActions');
  const permissions = useWorkspacePermissions();
  const dock = useDockCommands();
  const [url, setUrl] = useWorkspaceUrl();
  const editor = useWorkspaceEditor();
  const client = useClient();
  const remoteNode = useRemoteNode();
  const { showError } = useErrorModal();
  const openSubRunEntry = useSubRunOpener();

  const [statusStep, setStatusStep] = useState<string | null>(null);
  const [retryStepName, setRetryStepName] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<RunConfirm | null>(null);
  const [followed, setFollowed] = useState<readonly FollowedStepRun[]>(NONE);
  const clearFollowed = useCallback(() => setFollowed(NONE), []);
  const [picker, setPicker] = useState<{
    node: ApiNode;
    holder: ApiDAGRunDetails;
  } | null>(null);

  const details = run.details;
  const { handle } = editor;
  // The buffer is read when the confirmation opens: no edit can happen
  // while the dialog is open.
  const editorActions = useEditorRunActions({
    fileName,
    dagName: dagData.dag?.name ?? fileName,
    spec: confirm !== null ? (handle?.getBuffer() ?? '') : '',
    isDirty: confirm !== null ? (handle?.isDirty() ?? false) : false,
  });

  const runError = editorActions.error;
  useEffect(() => {
    if (runError) showError(runError);
  }, [runError, showError]);

  const retryGateOf = useCallback(
    (node: ApiNode) =>
      canRetryStep(details, node, {
        runDags: permissions.canRunDags,
        rootRunning: false,
      }),
    [details, permissions.canRunDags]
  );

  const can = useCallback(
    (step: string): StepRunGates => {
      const node = run.nodeByStep.get(step);
      const runStep = permissions.canExecute;
      if (!node) {
        return { retry: false, setStatus: false, runStep, subRuns: false };
      }
      const retryGate = retryGateOf(node);
      const statusGate = canChangeStepStatus(details, node, {
        runDags: permissions.canRunDags,
      });
      const retry = retryGate.ok && permissions.canRunDags;
      const setStatus = statusGate.ok && permissions.canRunDags;
      const reason =
        (retryGate.disabled ? retryGate.reason : undefined) ??
        (setStatus ? undefined : statusGate.reason);
      return {
        retry,
        setStatus,
        runStep,
        subRuns: allSubRunsOf(node).length > 0,
        ...(reason ? { reason } : {}),
      };
    },
    [details, permissions, retryGateOf, run.nodeByStep]
  );

  const retryStep = useCallback(
    (step: string) => {
      if (can(step).retry) setRetryStepName(step);
    },
    [can]
  );

  const setStatus = useCallback(
    (step: string) => {
      if (can(step).setStatus) setStatusStep(step);
    },
    [can]
  );

  // The definition that a run executes: the draft while the editor holds
  // unsaved text (a test run of it), else the saved file. Its steps give the
  // previous steps of a step.
  const runDefinition = useCallback(
    (): ApiDAGDetails | undefined =>
      (handle?.isDirty() ? handle.getDag() : undefined) ?? dagData.dag,
    [dagData.dag, handle]
  );

  // The YAML of the step: the editor text when there is one (an unsaved edit
  // counts), else the saved definition.
  const stepTextOf = useCallback(
    (step: string, dag: ApiDAGDetails | undefined): string => {
      const saved = dag?.steps?.find((s) => s.name === step);
      const buffer = handle?.getBuffer();
      if (buffer && dag) {
        const model = parseDagDoc(buffer);
        const key = resolveStepNames(model, dag).byName[step];
        const entry = model.steps.find((s) => s.key === key);
        if (entry) return buffer.slice(entry.range.from, entry.range.to);
      }
      return saved ? JSON.stringify(saved) : '';
    },
    [handle]
  );

  // One dialog for 'Run step', on the node and in the step view.
  const openRun = useCallback(
    (step: string) => {
      if (!can(step).runStep) return;
      const dag = runDefinition();
      const steps = dag?.steps;
      const reads = referencedStepNames(steps, step, stepTextOf(step, dag));
      // The steps that the step needs and reads from, with the steps that
      // they need, in dependency order.
      const previous = stepsWithUpstream(
        steps,
        [...upstreamStepNames(steps, step), ...reads],
        step
      );
      setConfirm({
        kind: 'step',
        step,
        previous,
        reads,
        followedRunId:
          followed.find((f) => f.kind === 'upstream' && f.step === step)
            ?.runId ?? null,
        liveText: handle?.getBuffer() || undefined,
      });
    },
    [can, followed, handle, runDefinition, stepTextOf]
  );
  const runStep = openRun;

  const runUpstream = useCallback(
    (step: string) => {
      if (!can(step).runStep) return;
      const upstream = upstreamStepNames(
        runDefinition()?.steps,
        step,
        'dependency'
      );
      if (upstream.length === 0) return;
      setConfirm({
        kind: 'upstream',
        step,
        previous: upstream,
        reads: [],
        followedRunId: null,
        liveText: undefined,
      });
    },
    [can, runDefinition]
  );

  const openSubRun = useCallback(
    (step: string, e?: { newTab?: boolean }) => {
      const node = run.nodeByStep.get(step);
      const children = allSubRunsOf(node);
      if (!node || !details || children.length === 0) return;
      if (children.length > 1) {
        setPicker({ node, holder: details });
        return;
      }
      openSubRunEntry({ node, index: 0, holder: details, newTab: e?.newTab });
    },
    [details, openSubRunEntry, run.nodeByStep]
  );

  const openLogs = useCallback((t: StepRunTarget) => dock.openStep(t), [dock]);

  const submitStatus = useCallback(
    async (step: components['schemas']['Step'], status: NodeStatus) => {
      if (!details) return;
      const sub = isSubDAGRun(details);
      const path = {
        name: sub ? details.rootDAGRunName : details.name,
        dagRunId: sub ? details.rootDAGRunId : details.dagRunId,
        stepName: step.name,
      };
      const { error } = sub
        ? await client.PATCH(
            '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/status',
            {
              params: {
                path: { ...path, subDAGRunId: details.dagRunId },
                query: { remoteNode },
              },
              body: { status },
            }
          )
        : await client.PATCH(
            '/dag-runs/{name}/{dagRunId}/steps/{stepName}/status',
            {
              params: { path, query: { remoteNode } },
              body: { status },
            }
          );
      if (error) {
        showError(
          error.message || ts('Failed to update status'),
          ts('Please try again or check the server connection.')
        );
        return;
      }
      run.applyNodeStatus(step.name, status);
      setStatusStep(null);
      await run.refresh();
    },
    [client, details, remoteNode, run, showError, ts]
  );

  // The step view of the step is open: its columns can follow the run.
  const viewStep = stay && url.ndv !== null ? url.step : null;

  // The editor holds unsaved text. The steps of a draft run as a test run
  // that leaves no trace (see useEditorRunActions).
  const dirty = confirm !== null && (handle?.isDirty() ?? false);
  const ephemeral = dirty;

  const confirmRun = useCallback(
    async (
      pick: Pick<
        StepRunPick,
        'steps' | 'reuseFrom' | 'outputsFromRuns' | 'reused'
      >
    ) => {
      const pending = confirm;
      setConfirm(null);
      if (pending === null) return;
      const { kind, step } = pending;
      const { steps, reuseFrom, outputsFromRuns, reused } = pick;
      const inputs = {
        ...(reuseFrom !== null ? { reuseOutputsFrom: reuseFrom } : {}),
        ...(outputsFromRuns !== null ? { outputsFromRuns } : {}),
        ...(reused.length > 0 ? { reused } : {}),
      };
      const id = await editorActions.runSteps(
        steps,
        Object.keys(inputs).length > 0 ? inputs : undefined
      );
      if (id === null) return;
      // The timeline polls the history for the test runs while this tab has
      // started one: the server sends no event for them.
      if (ephemeral) noteTestRunStarted(fileName);
      // A test run is in no list but the timeline: the step view that follows
      // it shows it too, so the step view opens when it is closed.
      if (ephemeral && stay && viewStep !== step) {
        setUrl({ step, ndv: 'expanded' }, { replace: true });
      }
      if (viewStep === step || (ephemeral && stay)) {
        // The user stays on the step; its columns follow the new run.
        const startedAt = Date.now();
        // What the run uses from before, kept with each entry: the INPUT
        // column shows these values and sources.
        const used = reused.length > 0 ? { reused } : {};
        const entries: FollowedStepRun[] = [];
        if (kind === 'upstream') {
          entries.push({
            kind: 'upstream',
            step,
            runId: id,
            steps,
            startedAt,
            ...used,
          });
        } else {
          // The steps that the request lists before the step.
          const before = steps.filter((name) => name !== step);
          if (before.length > 0) {
            entries.push({
              kind: 'upstream',
              step,
              runId: id,
              steps: before,
              startedAt,
              ...used,
            });
          }
          entries.push({
            kind: 'execute',
            step,
            runId: id,
            steps: [step],
            startedAt,
            ...used,
          });
        }
        // The data of the other kind stays until its own next run.
        setFollowed((list) => [
          ...list.filter((run) => !entries.some((e) => e.kind === run.kind)),
          ...entries,
        ]);
      }
      // One rule for every start, a test run too: the new run replaces the
      // run in the URL, with no history entry, because the URL always names
      // the shown run and does not follow a newer one. No mode, no ?ndv.
      setUrl({ run: id }, { replace: true });
      void refresh();
    },
    [
      confirm,
      editorActions,
      ephemeral,
      fileName,
      refresh,
      setUrl,
      stay,
      viewStep,
    ]
  );

  const statusNode =
    statusStep !== null ? run.nodeByStep.get(statusStep) : undefined;
  const retryNode =
    retryStepName !== null ? run.nodeByStep.get(retryStepName) : undefined;
  const dialogs = createElement(
    Fragment,
    null,
    statusNode
      ? createElement(StatusUpdateModal, {
          visible: true,
          step: statusNode.step,
          dismissModal: () => setStatusStep(null),
          onSubmit: submitStatus,
        })
      : null,
    retryNode && details
      ? createElement(StepRetryDialog, {
          open: true,
          onOpenChange: (open: boolean) => {
            if (!open) setRetryStepName(null);
          },
          dagRun: details,
          stepName: retryNode.step.name,
          disabled: retryGateOf(retryNode).disabled,
          onRetried: () => void run.refresh(),
        })
      : null,
    confirm === null
      ? null
      : confirm.kind === 'step' && confirm.previous.length > 0
        ? createElement(StepRunReuseDialog, {
            title: ts('Run step'),
            fileName,
            step: confirm.step,
            previous: confirm.previous,
            reads: confirm.reads,
            followedUpstreamRunId: confirm.followedRunId,
            ...(confirm.liveText !== undefined
              ? { liveText: confirm.liveText }
              : {}),
            unsaved: dirty,
            onPick: (pick: StepRunPick) => void confirmRun(pick),
            onCancel: () => setConfirm(null),
            onSelectRun: (id: string) => {
              setConfirm(null);
              setUrl({ run: id });
            },
          })
        : createElement(StepRunConfirmDialog, {
            title:
              confirm.kind === 'upstream'
                ? ts('Run upstream steps')
                : ts('Run step'),
            open: true,
            primary: 'run',
            choices: [
              {
                id: 'run',
                label: ts('Run'),
                onSelect: () =>
                  void confirmRun({
                    steps:
                      confirm.kind === 'upstream'
                        ? confirm.previous
                        : [confirm.step],
                    reuseFrom: null,
                    outputsFromRuns: null,
                    reused: [],
                  }),
              },
            ],
            onCancel: () => setConfirm(null),
            children: createElement(
              'div',
              { className: 'space-y-1 text-sm' },
              createElement('p', null, confirmQuestion(confirm, ts)),
              dirty
                ? createElement(
                    'p',
                    { className: 'text-muted-foreground' },
                    ts('The step runs from the unsaved editor text.')
                  )
                : null
            ),
          }),
    renderParallelPicker(picker, () => setPicker(null), openSubRunEntry)
  );

  return {
    can,
    retryStep,
    setStatus,
    runStep,
    runUpstream,
    followed,
    clearFollowed,
    openSubRun,
    openLogs,
    dialogs,
  };
}

const NONE: readonly FollowedStepRun[] = [];

/** What a confirmation waits for. */
interface RunConfirm {
  /** 'step': the choices of the step run. 'upstream': the previous steps only. */
  kind: 'step' | 'upstream';
  step: string;
  /**
   * Server names of the steps before the step (its upstream steps and the
   * steps that it reads from), in dependency order. For 'upstream', the
   * steps to run.
   */
  previous: string[];
  /** The steps whose outputs the step reads. */
  reads: string[];
  /** The upstream run that the step view follows, when there is one. */
  followedRunId: string | null;
  /** The editor text at the moment of the question. */
  liveText: string | undefined;
}

type Translate = ReturnType<typeof useI18n>['ts'];

function confirmQuestion(confirm: RunConfirm, ts: Translate): string {
  const { kind, step, previous } = confirm;
  if (kind === 'upstream') {
    return ts(
      'Run the {count} upstream steps of {name} now? Their commands run for real.',
      { name: step, count: String(previous.length) }
    );
  }
  return ts('Run only the step {name} now? Its command runs for real.', {
    name: step,
  });
}
