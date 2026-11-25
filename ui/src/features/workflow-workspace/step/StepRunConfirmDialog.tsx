// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as DialogPrimitive from '@radix-ui/react-dialog';
import { Check, ListChecks, Play, Target, X } from 'lucide-react';
import {
  useEffect,
  useMemo,
  useRef,
  type ReactElement,
  type ReactNode,
} from 'react';
import {
  CarriedStepCheckStatus,
  StepChangeKind,
  type components,
} from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/spinner';
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  reusedInputs,
  type InputProvenance,
  type StepRunRef,
} from '@/features/visual-editor/run/inputProvenance';
import { RunSourcePicker } from '@/features/visual-editor/run/RunSourcePicker';
import {
  composeCarriers,
  defaultChoice,
  planRun,
  type CarrierPlan,
  type RunChoice,
  type RunPlan,
} from '@/features/visual-editor/run/reuseSource';
import {
  useCacheScope,
  useOutputCacheEntries,
} from '@/features/visual-editor/run/useOutputCacheEntries';
import { useI18n } from '@/i18n/I18nProvider';
import { formatStepSpan } from '@/lib/formatStepSpan';
import dayjs from '@/lib/dayjs';
import { shortRunId } from '@/lib/shortRunId';
import { cn } from '@/lib/utils';
import { useWorkspaceData } from '../data/context';
import { Z } from '../zLayers';
import { useReuseCheck } from './useReuseCheck';
import { useReuseSource } from './useReuseSource';

type CarriedStepCheck = components['schemas']['CarriedStepCheck'];

/** One answer to the question, as a button after Cancel. */
export interface StepRunChoice {
  id: RunChoice;
  label: string;
  /** Called when the user picks the choice. */
  onSelect(): void;
  disabled?: boolean;
  /** A spinner takes the place of the icon. */
  busy?: boolean;
  /** The reason that the choice is disabled. */
  title?: string;
}

const CHOICE_ICON = { only: Target, changed: ListChecks, run: Play } as const;

/**
 * The question of a step run. The node view is on Z.ndv and a shared dialog
 * is under it, so the overlay and the content sit on Z.dialog here. The frame
 * is the one of ConfirmDialog; it has more buttons, and `primary` is the one
 * that has the focus, so that Enter picks it. While `primary` is null, no
 * button has the focus; it moves to the primary button when there is one.
 */
export function StepRunConfirmDialog({
  open,
  title,
  choices,
  primary,
  onCancel,
  children,
}: {
  open: boolean;
  title: string;
  choices: readonly StepRunChoice[];
  primary: RunChoice | null;
  onCancel(): void;
  children: ReactNode;
}): ReactElement {
  const { ts } = useI18n();
  const content = useRef<HTMLDivElement>(null);
  // The button that this dialog focused last. The focus follows the primary
  // choice while it is still there; a user who moved it keeps it.
  const focused = useRef<HTMLElement | null>(null);
  const focusPrimary = () => {
    const root = content.current;
    const target = root?.querySelector<HTMLElement>('[data-confirm]');
    if (!root || !target) return;
    const active = document.activeElement;
    const free =
      active === root ||
      active === document.body ||
      active === focused.current ||
      !root.contains(active);
    if (!free) return;
    target.focus({ preventScroll: true });
    focused.current = target;
  };
  useEffect(() => {
    if (open && primary !== null) focusPrimary();
  }, [open, primary]);

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onCancel()}>
      <DialogPortal>
        <DialogOverlay data-testid="step-run-overlay" className={Z.dialog} />
        <DialogPrimitive.Content
          ref={content}
          data-testid="step-run-dialog"
          // The primary button has the focus, as in ConfirmDialog: Enter runs.
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            focusPrimary();
          }}
          className={cn(
            'fixed left-1/2 top-1/2 grid w-full max-w-[500px] -translate-x-1/2 -translate-y-1/2 gap-4 border border-border bg-card p-6 shadow-lg focus:outline-none sm:rounded-xl',
            Z.dialog
          )}
        >
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription className="sr-only">
              {ts('Confirm the requested action.')}
            </DialogDescription>
          </DialogHeader>
          <div className="py-4">{children}</div>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onCancel}>
              <X className="h-4 w-4" aria-hidden="true" />
              {ts('Cancel')}
            </Button>
            {choices.map((choice) => {
              const isPrimary = choice.id === primary;
              const Icon = isPrimary ? Check : CHOICE_ICON[choice.id];
              const button = (
                <Button
                  key={choice.id}
                  type="button"
                  variant={isPrimary ? 'primary' : 'outline'}
                  data-testid={`step-run-${choice.id}`}
                  {...(isPrimary ? { 'data-confirm': '' } : {})}
                  disabled={choice.disabled}
                  onClick={choice.onSelect}
                >
                  {choice.busy ? (
                    <Spinner className="size-4" />
                  ) : (
                    <Icon className="h-4 w-4" aria-hidden="true" />
                  )}
                  {choice.label}
                </Button>
              );
              // A disabled button gets no pointer events: its reason sits on
              // a wrapper.
              return choice.title ? (
                <span key={choice.id} title={choice.title}>
                  {button}
                </span>
              ) : (
                button
              );
            })}
          </DialogFooter>
        </DialogPrimitive.Content>
      </DialogPortal>
    </Dialog>
  );
}

/** What a button of the reuse dialog asks for. */
export interface StepRunPick {
  choice: RunChoice;
  /** The `steps` of the request. */
  steps: string[];
  /** The run to copy outputs from; null sends no reuse. */
  reuseFrom: string | null;
  /**
   * The run that each cached previous step that the request skips is read
   * from, by step name (`outputsFromRuns`); null sends none. The browser
   * sends no value.
   */
  outputsFromRuns: Record<string, string> | null;
  /**
   * The skipped steps that changed since the run that gives them and that
   * the user reuses anyway (`allowChangedSteps`).
   */
  allowChangedSteps: string[];
  /** Where the outputs of the previous steps that the request skips come from. */
  reused: InputProvenance[];
}

// The changes that the row of a step names. The other changes are of the
// whole run, and one line names them for the run.
const STEP_CHANGES: readonly StepChangeKind[] = [
  StepChangeKind.step,
  StepChangeKind.dependency,
  StepChangeKind.carried,
];

// The noun of each change for a list; a run that cannot be compared has a
// row of its own instead.
const CHANGE_LABEL: Readonly<Partial<Record<StepChangeKind, string>>> = {
  [StepChangeKind.step]: 'the step',
  [StepChangeKind.dependency]: 'an upstream step',
  [StepChangeKind.params]: 'the parameters',
  [StepChangeKind.profile]: 'the runtime profile',
  [StepChangeKind.baseConfig]: 'the base config',
  [StepChangeKind.settings]: 'the top-level settings',
  [StepChangeKind.carried]: 'outputs of a changed step',
};

/**
 * The dialog of "Run step" for a step that has previous steps: the run that
 * feeds the step, the state of each previous step, and the choices Step only,
 * Changed steps and Run. The server checks each previous step against the
 * run that gives it, as the start will (`useReuseCheck`), and the rows name
 * what changed. Step only reuses the changed steps anyway; Changed steps runs
 * them. Until the server answers, the browser's own view stands; when the
 * check fails, the dialog says so and keeps that view. The default button
 * comes from `defaultChoice`; the `steps` of each button come from `planRun`.
 */
export function StepRunReuseDialog({
  title,
  fileName,
  step,
  previous,
  reads,
  followedUpstreamRunId,
  liveText,
  unsaved,
  refused = NO_REFUSALS,
  onPick,
  onCancel,
  onSelectRun,
}: {
  title: string;
  fileName: string;
  step: string;
  /** The steps before the step, in dependency order. */
  previous: readonly string[];
  /** The steps whose outputs the step reads. */
  reads: readonly string[];
  followedUpstreamRunId: string | null;
  liveText?: string;
  /** The step runs from unsaved editor text. */
  unsaved: boolean;
  /**
   * The carried steps that the server refused when the user last picked a
   * choice: something changed after the check. The dialog shows why.
   */
  refused?: readonly CarriedStepCheck[];
  onPick(pick: StepRunPick): void;
  onCancel(): void;
  onSelectRun(runId: string): void;
}): ReactElement {
  const { ts } = useI18n();
  const reuse = useReuseSource({
    fileName,
    step,
    previous,
    reads,
    followedUpstreamRunId,
    ...(liveText !== undefined ? { liveText } : {}),
  });
  const { source, waitingFor } = reuse;
  const sourceId = source?.dagRunId ?? null;

  // The server checks the source run and every cache entry of a previous
  // step, also one that the browser finds stale: the user may reuse it.
  const { dagData } = useWorkspaceData('StepRunReuseDialog');
  const cache = useOutputCacheEntries(useCacheScope(fileName));
  const entryRuns = useMemo(
    () =>
      Object.fromEntries(
        previous.flatMap((name) => {
          const entry = cache[name];
          return name !== step && entry ? [[name, entry.runId]] : [];
        })
      ),
    [cache, previous, step]
  );
  const check = useReuseCheck({
    fileName,
    dagName: dagData.dag?.name ?? fileName,
    step,
    sourceRunId: sourceId,
    entries: entryRuns,
    draft: unsaved ? (liveText ?? null) : null,
    enabled: !reuse.loading,
  });
  const verified: CarrierPlan | null =
    check.state === 'ready'
      ? composeCarriers({
          previous,
          fromSource: sourceId !== null ? check.fromSource : {},
          fromCache: check.fromCache,
        })
      : null;
  const loading = reuse.loading || check.state === 'checking';

  // Without the server's answer, the browser's view stands, with the steps
  // that the server refused last.
  const refusedChanged = refused
    .filter((r) => r.status === CarriedStepCheckStatus.changed)
    .map((r) => r.step);
  const refusedGone = refused
    .filter((r) => r.status === CarriedStepCheckStatus.unavailable)
    .map((r) => r.step);
  const stale = verified
    ? verified.changed
    : union(reuse.stale, refusedChanged);
  const missing = verified
    ? verified.missing
    : union(reuse.missing, refusedGone);
  const cachedRuns: Readonly<Record<string, { runId: string }>> = verified
    ? Object.fromEntries(
        Object.entries(verified.carriers).flatMap(([name, carrier]) =>
          carrier.from === 'cache' ? [[name, { runId: carrier.runId }]] : []
        )
      )
    : Object.fromEntries(
        Object.entries(reuse.cachedRuns).filter(
          ([name]) => !refusedGone.includes(name)
        )
      );
  const cached = previous.filter((name) => name in cachedRuns);
  const covered = cached.length;
  // The run of each previous step that a run gives, with the times of the
  // step there.
  const stepRuns: Readonly<Record<string, StepRunRef>> = verified
    ? Object.fromEntries(
        Object.entries(verified.carriers).map(([name, carrier]) => [
          name,
          {
            runId: carrier.runId,
            ...(carrier.startedAt ? { startedAt: carrier.startedAt } : {}),
            ...(carrier.finishedAt ? { finishedAt: carrier.finishedAt } : {}),
          },
        ])
      )
    : reuse.stepRuns;

  const plan: RunPlan = planRun({
    previous,
    stale,
    missing,
    step,
    order: previous,
  });
  const hasChanged =
    (source !== null || covered > 0) &&
    (stale.length > 0 || missing.length > 0);
  const primary: RunChoice | null = loading
    ? null
    : defaultChoice({ source, stale, missing, covered });
  const pick = (choice: RunChoice) => () => {
    const steps =
      choice === 'only'
        ? plan.only
        : choice === 'changed'
          ? plan.changed
          : plan.full;
    // The server refuses a run for a step that the request runs.
    const fromRuns = Object.fromEntries(
      Object.entries(cachedRuns)
        .filter(([name]) => !steps.includes(name))
        .map(([name, ref]) => [name, ref.runId])
    );
    const reuseFrom = choice === 'run' ? null : sourceId;
    const sending = choice !== 'run' && Object.keys(fromRuns).length > 0;
    onPick({
      choice,
      steps,
      reuseFrom,
      outputsFromRuns: sending ? fromRuns : null,
      // A changed step that the request skips is one that the user chose
      // to reuse as it is.
      allowChangedSteps:
        choice === 'run' ? [] : stale.filter((name) => !steps.includes(name)),
      reused:
        choice === 'run'
          ? []
          : reusedInputs({
              previous,
              steps,
              fromRuns: sending ? pickRefs(stepRuns, fromRuns) : {},
              source: reuseFrom !== null ? source : null,
              stale,
              missing,
            }),
    });
  };

  const choices: StepRunChoice[] = [];
  // Step only needs a source, or a step that reads nothing. Without a
  // source it waits for an active run, or for the search to end.
  const offersOnly =
    loading ||
    waitingFor !== null ||
    source !== null ||
    covered > 0 ||
    reads.length === 0;
  if (offersOnly) {
    const wait = loading
      ? ts('Checking earlier runs')
      : waitingFor !== null
        ? ts('Waits for run {id} to finish', { id: waitingFor })
        : undefined;
    choices.push({
      id: 'only',
      label: ts('Step only'),
      onSelect: pick('only'),
      disabled: wait !== undefined,
      busy: wait !== undefined,
      ...(wait !== undefined ? { title: wait } : {}),
    });
  }
  if (!loading && hasChanged) {
    choices.push({
      id: 'changed',
      label: ts('Changed steps'),
      onSelect: pick('changed'),
    });
  }
  choices.push({ id: 'run', label: ts('Run'), onSelect: pick('run') });

  const changeList = (changes: readonly StepChangeKind[]) =>
    changes
      .flatMap((change) => {
        const label = CHANGE_LABEL[change];
        return label ? [ts(label)] : [];
      })
      .join(', ');
  // The run of a step that a run gives: its short id, what the step took
  // there and when it ended, so that the user sees what a run supplies.
  const detail = (name: string): ReactNode => {
    const ref = stepRuns[name];
    if (!ref) return null;
    const took = formatStepSpan(ref.startedAt, ref.finishedAt, {
      belowSecond: ts('<1s'),
    });
    const ended = ref.finishedAt ? dayjs(ref.finishedAt).format('HH:mm') : null;
    return (
      <span
        data-testid="run-step-run"
        className="text-muted-foreground"
        title={ref.runId}
      >
        {[ts('run {id}', { id: shortRunId(ref.runId) }), took, ended]
          .filter(Boolean)
          .join(', ')}
      </span>
    );
  };
  // The row of a step that the server checked. A cache entry whose run is
  // gone is named unless an unchanged run gives the step, as the browser's
  // view names it.
  const verifiedMark = (checked: CarrierPlan, name: string): ReactNode => {
    const carrier = checked.carriers[name];
    const gone = checked.unavailable[name];
    const goneMark =
      gone !== undefined && (!carrier || carrier.changes.length > 0) ? (
        <span data-testid="run-step-gone" className="text-warning" title={gone}>
          {ts('source run no longer available')}
        </span>
      ) : null;
    if (!carrier) {
      if (goneMark) return goneMark;
      return sourceId === null ? null : (
        <span className="text-warning" title={sourceId}>
          {ts('no output in run {id}', { id: shortRunId(sourceId) })}
        </span>
      );
    }
    const id = shortRunId(carrier.runId);
    if (carrier.changes.length === 0) {
      return (
        <>
          <span
            data-testid={
              carrier.from === 'cache' ? 'run-step-mark-cached' : undefined
            }
            className="text-muted-foreground"
          >
            {carrier.from === 'cache' ? ts('cached') : ts('reused')}
          </span>
          {detail(name)}
        </>
      );
    }
    const own = carrier.changes.filter((change) =>
      STEP_CHANGES.includes(change)
    );
    const text = carrier.changes.includes(StepChangeKind.unknown)
      ? ts('cannot compare with run {id}', { id })
      : own.length > 0
        ? ts('changed since run {id}: {changes}', {
            id,
            changes: changeList(own),
          })
        : ts('changed since run {id}', { id });
    return (
      <>
        {goneMark}
        <span
          data-testid="run-step-changed"
          className="text-warning"
          title={carrier.message ?? carrier.runId}
        >
          {text}
        </span>
      </>
    );
  };
  const mark = (name: string): ReactNode => {
    if (verified) return verifiedMark(verified, name);
    if (cached.includes(name)) {
      return (
        <>
          <span
            data-testid="run-step-mark-cached"
            className="text-muted-foreground"
          >
            {ts('cached')}
          </span>
          {detail(name)}
        </>
      );
    }
    if (reuse.gone.includes(name)) {
      return (
        <span data-testid="run-step-gone" className="text-warning">
          {ts('source run no longer available')}
        </span>
      );
    }
    if (sourceId === null) return null;
    if (missing.includes(name)) {
      return (
        <span className="text-warning" title={sourceId}>
          {ts('no output in run {id}', { id: shortRunId(sourceId) })}
        </span>
      );
    }
    if (stale.includes(name)) {
      return (
        <span className="text-warning" title={sourceId}>
          {ts('changed since run {id}', { id: shortRunId(sourceId) })}
        </span>
      );
    }
    return (
      <>
        <span className="text-muted-foreground">{ts('reused')}</span>
        {detail(name)}
      </>
    );
  };
  const dot = (name: string): string =>
    (verified !== null || sourceId !== null || covered > 0) &&
    (missing.includes(name) || stale.includes(name))
      ? 'bg-warning'
      : 'bg-muted-foreground/40';

  // What changed for every step of a run: the parameters, the profile, the
  // base config, or the top-level settings, in one line for each run.
  const runChanges = verified ? changesOfRuns(verified) : [];

  return (
    <StepRunConfirmDialog
      open
      title={title}
      choices={choices}
      primary={primary}
      onCancel={onCancel}
    >
      <div className="space-y-3 text-sm">
        {refused.length > 0 ? (
          <div data-testid="run-step-refused" className="text-warning">
            <p>
              {ts(
                'The run did not start because something changed after the check:'
              )}
            </p>
            <ul className="list-disc pl-5">
              {refused.map((r) => (
                <li key={`${r.step}\u0000${r.runId}`}>{r.message ?? r.step}</li>
              ))}
            </ul>
          </div>
        ) : null}
        <SourceLine
          reuse={reuse}
          readsOutputs={reads.length > 0}
          onSelectRun={onSelectRun}
        />
        {check.state === 'failed' && check.error !== null ? (
          <p data-testid="run-step-check-failed" className="text-warning">
            {ts('Could not check the earlier runs: {message}', {
              message: check.error,
            })}{' '}
            <button
              type="button"
              className="rounded-sm underline underline-offset-2 hover:text-primary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              onClick={check.retry}
            >
              {ts('Check again')}
            </button>
          </p>
        ) : null}
        {runChanges.map(({ runId, changes }) => (
          <p
            key={runId}
            data-testid="run-step-run-changes"
            className="text-warning"
            title={runId}
          >
            {ts('Since run {id}, {changes} changed.', {
              id: shortRunId(runId),
              changes: changeList(changes),
            })}
          </p>
        ))}
        {!verified && reuse.paramsDiffer && sourceId !== null ? (
          <p className="text-warning" title={sourceId}>
            {ts(
              'Run {id} used other parameters. The step runs with the defaults.',
              {
                id: shortRunId(sourceId),
              }
            )}
          </p>
        ) : null}
        <ul
          data-testid="run-steps-list"
          className="max-h-40 space-y-1 overflow-y-auto"
        >
          {previous.map((name) => (
            <li
              key={name}
              data-testid="run-step-item"
              className="flex min-w-0 items-center gap-2"
            >
              <span
                aria-hidden="true"
                className={cn('size-1.5 shrink-0 rounded-full', dot(name))}
              />
              <span className="min-w-0 truncate">{name}</span>
              {mark(name)}
            </li>
          ))}
        </ul>
        {offersOnly && !loading && stale.length > 0 ? (
          <p data-testid="run-step-consent" className="text-muted-foreground">
            {ts(
              'Step only reuses the outputs of the changed steps as they are.'
            )}
          </p>
        ) : null}
        {unsaved ? (
          <p className="text-muted-foreground">
            {ts('The step runs from the unsaved editor text.')}
          </p>
        ) : null}
      </div>
    </StepRunConfirmDialog>
  );
}

const NO_REFUSALS: readonly CarriedStepCheck[] = [];

/** The names of a and of b, each once, a first. */
function union(a: readonly string[], b: readonly string[]): string[] {
  return [...new Set([...a, ...b])];
}

/**
 * The changes of the whole run for each run that gives a changed step, in
 * the order of the steps: what the rows leave out.
 */
function changesOfRuns(
  plan: CarrierPlan
): { runId: string; changes: StepChangeKind[] }[] {
  const byRun = new Map<string, Set<StepChangeKind>>();
  for (const carrier of Object.values(plan.carriers)) {
    for (const change of carrier.changes) {
      if (STEP_CHANGES.includes(change) || change === StepChangeKind.unknown) {
        continue;
      }
      const changes = byRun.get(carrier.runId) ?? new Set<StepChangeKind>();
      byRun.set(carrier.runId, changes.add(change));
    }
  }
  const order = Object.values(StepChangeKind);
  return [...byRun].map(([runId, changes]) => ({
    runId,
    changes: order.filter((change) => changes.has(change)),
  }));
}

/** The refs of the steps that the request gives to a run of their own. */
function pickRefs(
  refs: Readonly<Record<string, StepRunRef>>,
  fromRuns: Readonly<Record<string, string>>
): Record<string, StepRunRef> {
  return Object.fromEntries(
    Object.keys(fromRuns).flatMap((name) => {
      const ref = refs[name];
      return ref ? [[name, ref]] : [];
    })
  );
}

/**
 * The source of the inputs: the run picker, with a link that shows the run
 * that the step run uses. With no run to choose, the reason: the step waits
 * for an active run, or no earlier run holds what the step reads.
 */
function SourceLine({
  reuse,
  readsOutputs,
  onSelectRun,
}: {
  reuse: ReturnType<typeof useReuseSource>;
  /** The step reads outputs, so a missing source is a problem. */
  readsOutputs: boolean;
  onSelectRun(runId: string): void;
}): ReactElement | null {
  const { ts } = useI18n();
  const { source, waitingFor, loading, options } = reuse;
  if (loading) return null;
  if (options.length === 0) {
    if (waitingFor !== null) {
      return (
        <p className="text-muted-foreground" title={waitingFor}>
          {ts('Waits for run {id} to finish', { id: shortRunId(waitingFor) })}
        </p>
      );
    }
    return readsOutputs ? (
      <p className="text-warning">
        {ts('No earlier run holds the outputs that this step reads.')}
      </p>
    ) : null;
  }
  return (
    <div
      className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-muted-foreground"
      data-testid="run-source"
    >
      <span>{ts('Inputs from')}</span>
      <RunSourcePicker
        options={options}
        selectedId={reuse.selectedOptionId}
        onSelect={reuse.choose}
      />
      {source && (
        <button
          type="button"
          data-testid="run-source-link"
          className="rounded-sm underline underline-offset-2 hover:text-primary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          title={source.dagRunId}
          onClick={() => onSelectRun(source.dagRunId)}
        >
          {ts('Show run')}
        </button>
      )}
    </div>
  );
}
