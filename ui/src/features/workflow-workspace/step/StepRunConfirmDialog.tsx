// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as DialogPrimitive from '@radix-ui/react-dialog';
import { Check, ListChecks, Play, Target, X } from 'lucide-react';
import { useEffect, useRef, type ReactElement, type ReactNode } from 'react';
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
  defaultChoice,
  planRun,
  type RunChoice,
  type RunPlan,
} from '@/features/visual-editor/run/reuseSource';
import { useI18n } from '@/i18n/I18nProvider';
import { formatStepSpan } from '@/lib/formatStepSpan';
import dayjs from '@/lib/dayjs';
import { shortRunId } from '@/lib/shortRunId';
import { cn } from '@/lib/utils';
import { Z } from '../zLayers';
import { useReuseSource } from './useReuseSource';

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
  /** Where the outputs of the previous steps that the request skips come from. */
  reused: InputProvenance[];
}

/**
 * The dialog of "Run step" for a step that has previous steps: the run that
 * feeds the step, the state of each previous step, and the choices Step only,
 * Changed steps and Run. The default button comes from `defaultChoice`; the
 * `steps` of each button come from `planRun`.
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
  const { source, waitingFor, stale, missing, loading } = reuse;
  const { cached } = reuse;
  const covered = cached.length;

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
      Object.entries(reuse.cachedRuns)
        .filter(([name]) => !steps.includes(name))
        .map(([name, ref]) => [name, ref.runId])
    );
    const reuseFrom = choice === 'run' ? null : (source?.dagRunId ?? null);
    const sending = choice !== 'run' && Object.keys(fromRuns).length > 0;
    onPick({
      choice,
      steps,
      reuseFrom,
      outputsFromRuns: sending ? fromRuns : null,
      reused:
        choice === 'run'
          ? []
          : reusedInputs({
              previous,
              steps,
              fromRuns: sending ? pickRefs(reuse.stepRuns, fromRuns) : {},
              source: reuseFrom !== null ? source : null,
              stale,
              missing,
            }),
    });
  };

  const choices: StepRunChoice[] = [];
  // Step only needs a source, or a step that reads nothing. Without a
  // source it waits for an active run, or for the search to end.
  if (
    loading ||
    waitingFor !== null ||
    source !== null ||
    covered > 0 ||
    reads.length === 0
  ) {
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

  const sourceId = source?.dagRunId ?? null;
  // The run of a step that a run gives: its short id, what the step took
  // there and when it ended, so that the user sees what a run supplies.
  const detail = (name: string): ReactNode => {
    const ref = reuse.stepRuns[name];
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
  const mark = (name: string): ReactNode => {
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
    sourceId !== null && (missing.includes(name) || stale.includes(name))
      ? 'bg-warning'
      : 'bg-muted-foreground/40';

  return (
    <StepRunConfirmDialog
      open
      title={title}
      choices={choices}
      primary={primary}
      onCancel={onCancel}
    >
      <div className="space-y-3 text-sm">
        <SourceLine
          reuse={reuse}
          readsOutputs={reads.length > 0}
          onSelectRun={onSelectRun}
        />
        {reuse.paramsDiffer && sourceId !== null ? (
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
        {unsaved ? (
          <p className="text-muted-foreground">
            {ts('The step runs from the unsaved editor text.')}
          </p>
        ) : null}
      </div>
    </StepRunConfirmDialog>
  );
}

/**
 * The source of the inputs: the run picker, with a link that shows the run
 * that the step run uses. With no run to choose, the reason: the step waits
 * for an active run, or no earlier run holds what the step reads.
 */
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
