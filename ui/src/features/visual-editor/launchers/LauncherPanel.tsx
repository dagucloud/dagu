// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ArrowLeftRight, Plus, SlidersHorizontal, Trash2 } from 'lucide-react';
import { useState, type ReactElement, type ReactNode } from 'react';
import type { components, UpcomingRunsHold } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { useConfig } from '@/contexts/ConfigContext';
import WebhookTab from '@/features/dags/components/dag-details/WebhookTab';
import {
  PANEL_BODY_CLASS,
  PanelHeader,
  usePanelColumn,
} from '@/features/dags/components/step-details/PanelShell';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { cn } from '@/lib/utils';
import {
  isServicePolicy,
  type RestartPolicy,
  type RestartView,
  type ScheduleView,
} from '../doc';
import { LAUNCHER_ICONS, LAUNCHER_LABELS } from './LauncherNode';
import type { LauncherId } from './model';
import { nextRuns, SCHEDULE_PRESETS } from './nextRuns';
import { ScheduleBuilder, ScheduleSummary } from './ScheduleBuilder';
import { UpcomingRuns } from './UpcomingRuns';

type Schedule = components['schemas']['Schedule'];
type UpcomingRun = components['schemas']['UpcomingRun'];

function Shell({
  id,
  onClose,
  children,
}: {
  id: LauncherId;
  onClose(): void;
  children: ReactNode;
}): ReactElement {
  const { ts } = useI18n();
  const Icon = LAUNCHER_ICONS[id];
  // In the host's right column the tab names the launcher and closes it.
  const inColumn = usePanelColumn() !== null;
  return (
    <div
      data-testid={`ve-launcher-panel-${id}`}
      className="flex min-h-0 flex-1 flex-col"
    >
      {!inColumn && (
        <PanelHeader
          title={
            <span className="flex items-center gap-2">
              <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
              {ts(LAUNCHER_LABELS[id])}
            </span>
          }
          subtitle={ts('Launcher')}
          closeLabel={ts('Close')}
          onClose={onClose}
        />
      )}
      <div className={`${PANEL_BODY_CLASS} space-y-4`}>{children}</div>
    </div>
  );
}

/** Manual launch is always on: the panel says how it works. */
export function ManualLauncherPanel({
  params,
  onClose,
}: {
  /** Parameters of the workflow, as written. */
  params: readonly string[];
  onClose(): void;
}): ReactElement {
  const { ts } = useI18n();
  return (
    <Shell id="manual" onClose={onClose}>
      <p className="text-sm text-foreground">
        {ts('Manual launch is always active')}
      </p>
      <p className="text-sm text-muted-foreground">
        {ts(
          'Use the Start button of the bar to run this workflow now. The run takes the parameters that you give in the Start dialog.'
        )}
      </p>
      <section className="space-y-1" aria-label={ts('Parameters')}>
        <h3 className="text-sm font-medium text-foreground">
          {ts('Parameters')}
        </h3>
        {params.length === 0 ? (
          <p className="text-sm italic text-muted-foreground">
            {ts('This workflow has no parameters')}
          </p>
        ) : (
          <ul className="space-y-1">
            {params.map((param, index) => (
              <li
                key={`${param}-${index}`}
                className="break-all rounded bg-muted px-2 py-1 font-mono text-xs"
              >
                {param}
              </li>
            ))}
          </ul>
        )}
      </section>
    </Shell>
  );
}

/** The next fire times of an expression, as the browser computes them. */
function NextRunsPreview({ expression }: { expression: string }): ReactElement {
  const { ts } = useI18n();
  const { tz } = useConfig();
  const runs = nextRuns(expression, 3, new Date(), tz);
  if (!runs) {
    return (
      <p className="text-xs text-muted-foreground">
        {ts('No preview for this expression')}
      </p>
    );
  }
  return (
    <p
      className="text-xs text-muted-foreground"
      data-testid="ve-launcher-next-runs"
    >
      {ts('Next runs:')}{' '}
      {runs
        .map(
          (run) =>
            formatAbsoluteTime(run.toISOString(), { seconds: false }) ?? '-'
        )
        .join(', ')}
    </p>
  );
}

function ExpressionRow({
  expression,
  editable,
  serverPreview,
  onCommit,
  onRemove,
}: {
  expression: string;
  editable: boolean;
  /** The panel lists the server's runs, so the row previews only drafts. */
  serverPreview: boolean;
  /** False when the panel refuses the expression: the draft goes back. */
  onCommit(next: string): boolean;
  onRemove(): void;
}): ReactElement {
  const { ts } = useI18n();
  const [draft, setDraft] = useState(expression);
  const [formOpen, setFormOpen] = useState(false);
  // The row stays mounted when its expression changes, so that an edit in
  // the form keeps the form open. A new expression replaces the draft.
  const [shown, setShown] = useState(expression);
  if (shown !== expression) {
    setShown(expression);
    setDraft(expression);
  }
  const previewed = !serverPreview || draft.trim() !== expression;
  const commit = () => {
    const next = draft.trim();
    if (next === expression) return;
    if (next === '' || !onCommit(next)) setDraft(expression);
  };
  return (
    <li className="space-y-1">
      <div className="flex items-center gap-2">
        <input
          type="text"
          value={draft}
          readOnly={!editable}
          aria-label={ts('Cron expression')}
          spellCheck={false}
          onChange={(event) => setDraft(event.target.value)}
          onBlur={commit}
          onKeyDown={(event) => {
            if (event.nativeEvent.isComposing) return;
            if (event.key === 'Enter') commit();
            if (event.key === 'Escape') setDraft(expression);
          }}
          className="h-8 min-w-0 flex-1 rounded-md border bg-background px-2 font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
        />
        {editable && (
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={ts('Edit with the form')}
            title={ts('Edit with the form')}
            aria-expanded={formOpen}
            onClick={() => setFormOpen((open) => !open)}
          >
            <SlidersHorizontal className="h-4 w-4" aria-hidden="true" />
          </Button>
        )}
        {editable && (
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={ts('Remove this expression')}
            title={ts('Remove this expression')}
            onClick={onRemove}
          >
            <Trash2 className="h-4 w-4" aria-hidden="true" />
          </Button>
        )}
      </div>
      {editable && formOpen && (
        <ScheduleBuilder
          expression={draft}
          onChange={(next) => {
            if (next === expression || onCommit(next)) setDraft(next);
          }}
        />
      )}
      <ScheduleSummary expression={draft} />
      {previewed && <NextRunsPreview expression={draft.trim()} />}
    </li>
  );
}

/**
 * The schedule of the workflow, as a list of cron expressions. An edit is
 * one change of the YAML text. The server's next runs, when given, cover
 * every start entry; without them each expression is previewed in the
 * browser.
 */
interface CronProps {
  schedule: ScheduleView;
  editable: boolean;
  /** True when `restart_policy` keeps the workflow running: no schedule can be added. */
  alwaysOn?: boolean;
  /** Next runs of the saved or validated schedule, from the server. */
  upcomingRuns?: UpcomingRun[];
  /** Why none of the upcoming runs will start, if they will not. */
  upcomingRunsHold?: UpcomingRunsHold;
  /** The server's start schedules, which upcomingRuns index. */
  schedules?: Schedule[];
  /** The new list of start expressions; the other entries stay. */
  onChange(expressions: string[]): void;
}

function CronBody({
  schedule,
  editable,
  upcomingRuns,
  upcomingRunsHold,
  schedules = [],
  alwaysOn = false,
  onChange,
  onSwitch,
}: CronProps & { onSwitch?: () => void }): ReactElement {
  const { ts } = useI18n();
  const [added, setAdded] = useState('');
  // A row keeps its position as key so that it stays mounted across its
  // own edits. A removal moves the rows after it to other positions, so it
  // remounts the rows and their state does not pass to another expression.
  const [removals, setRemovals] = useState(0);
  const { expressions } = schedule;
  const add = (expression: string) => {
    const next = expression.trim();
    if (next === '' || expressions.includes(next)) return;
    onChange([...expressions, next]);
    setAdded('');
  };
  return (
    <>
      {alwaysOn && (
        <p
          role="note"
          className="rounded-md border border-warning px-2 py-1.5 text-xs text-foreground"
        >
          {ts(
            'Not available with Always on: a workflow that is always on cannot have a schedule. Turn Always on off to use cron.'
          )}
        </p>
      )}
      <p className="text-sm text-muted-foreground">
        {ts('The workflow starts on each of these cron expressions.')}
      </p>
      {expressions.length === 0 ? (
        <p className="text-sm italic text-muted-foreground">
          {ts('No schedule defined')}
        </p>
      ) : (
        <ul className="space-y-3" aria-label={ts('Cron expressions')}>
          {expressions.map((expression, index) => (
            <ExpressionRow
              key={`${removals}-${index}`}
              expression={expression}
              editable={editable}
              serverPreview={upcomingRuns !== undefined}
              onCommit={(next) => {
                if (expressions.includes(next)) return false;
                onChange(expressions.map((e, i) => (i === index ? next : e)));
                return true;
              }}
              onRemove={() => {
                setRemovals((count) => count + 1);
                onChange(expressions.filter((_, i) => i !== index));
              }}
            />
          ))}
        </ul>
      )}
      {upcomingRuns && schedule.present && (
        <UpcomingRuns
          runs={upcomingRuns}
          hold={upcomingRunsHold}
          schedules={schedules}
        />
      )}
      {schedule.others > 0 && (
        <p className="text-xs text-muted-foreground">
          {ts(
            '{count} other schedule entries stay in the YAML. Edit them there.',
            { count: schedule.others }
          )}
        </p>
      )}
      {editable && !alwaysOn && (
        <section className="space-y-2" aria-label={ts('Add an expression')}>
          <ScheduleBuilder expression={added} onChange={setAdded} />
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={added}
              spellCheck={false}
              placeholder="0 * * * *"
              aria-label={ts('New cron expression')}
              onChange={(event) => setAdded(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
                  add(added);
                }
              }}
              className="h-8 min-w-0 flex-1 rounded-md border bg-background px-2 font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            />
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={added.trim() === ''}
              onClick={() => add(added)}
            >
              <Plus className="h-3.5 w-3.5" aria-hidden="true" />
              {ts('Add')}
            </Button>
          </div>
          {added.trim() !== '' && (
            <>
              <ScheduleSummary expression={added} />
              <NextRunsPreview expression={added.trim()} />
            </>
          )}
          <div className="flex flex-wrap gap-1.5">
            {SCHEDULE_PRESETS.map((preset) => (
              <button
                key={preset.expression}
                type="button"
                title={preset.expression}
                onClick={() => add(preset.expression)}
                className="rounded-md border px-2 py-1 text-xs text-foreground hover:bg-accent"
              >
                {ts(preset.label)}
              </button>
            ))}
          </div>
        </section>
      )}
      {editable && expressions.length > 0 && (
        <div className="space-y-1.5">
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="text-destructive"
            onClick={() => onChange([])}
          >
            <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
            {ts('Remove all cron expressions')}
          </Button>
          <p className="text-xs text-muted-foreground">
            {ts(
              'Other schedule entries (stop, restart, one-off) stay in the YAML.'
            )}
          </p>
        </div>
      )}
      {onSwitch && editable && (
        <SwitchButton label="Switch to Always on" onClick={onSwitch} />
      )}
    </>
  );
}

export function CronLauncherPanel({
  onClose,
  ...props
}: CronProps & { onClose(): void }): ReactElement {
  return (
    <Shell id="cron" onClose={onClose}>
      <CronBody {...props} />
    </Shell>
  );
}

const POLICIES: readonly RestartPolicy[] = ['no', 'always', 'unless-stopped'];
const POLICY_LABELS: Record<RestartPolicy, string> = {
  no: 'Off',
  always: 'Always',
  'unless-stopped': 'Unless stopped',
};
const POLICY_HINTS: Record<Exclude<RestartPolicy, 'no'>, string> = {
  always: 'A new run follows each run, whatever its status.',
  'unless-stopped':
    'A new run follows each run, except a run that an operator stopped.',
};
/** The wait that a new policy gets when the file has none: the policy needs one. */
const DEFAULT_WAIT_SEC = 5;

/** A field of a positive integer. A value that is not one goes back to the saved value. */
function CountField({
  label,
  value,
  placeholder,
  editable,
  optional,
  onCommit,
}: {
  label: string;
  value: number | null;
  placeholder?: string;
  editable: boolean;
  /** True when an empty field removes the value. */
  optional: boolean;
  onCommit(next: number | null): void;
}): ReactElement {
  const saved = value === null ? '' : String(value);
  const [draft, setDraft] = useState(saved);
  const commit = () => {
    const text = draft.trim();
    if (text === saved) return;
    if (text === '' && optional) onCommit(null);
    else if (/^[0-9]+$/.test(text) && Number(text) >= 1) onCommit(Number(text));
    else setDraft(saved);
  };
  return (
    <label className="flex items-center justify-between gap-3 text-sm">
      <span className="text-foreground">{label}</span>
      <input
        type="text"
        inputMode="numeric"
        value={draft}
        readOnly={!editable}
        placeholder={placeholder}
        spellCheck={false}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.nativeEvent.isComposing) return;
          if (event.key === 'Enter') commit();
          if (event.key === 'Escape') setDraft(saved);
        }}
        className="h-8 w-24 rounded-md border bg-background px-2 text-right font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
      />
    </label>
  );
}

/**
 * The restart policy of the workflow: the scheduler keeps one run alive and
 * starts a new one when it ends. It cannot go with a schedule or a
 * retry_policy, so the panel does not turn it on while the file has one.
 */
interface AlwaysProps {
  restart: RestartView;
  editable: boolean;
  /** A field that is left out stays; `null` removes the key. */
  onChange(change: {
    policy?: RestartPolicy;
    waitSec?: number | null;
    histRetentionRuns?: number | null;
  }): void;
}

function AlwaysBody({
  restart,
  editable,
  onChange,
  onSwitch,
}: AlwaysProps & { onSwitch?: () => void }): ReactElement {
  const { ts } = useI18n();
  const on = isServicePolicy(restart.policy);
  const conflict = restart.retryPolicy
    ? ts(
        'Remove the retry_policy of the workflow first: a workflow that is always on cannot have one.'
      )
    : null;
  const choose = (policy: RestartPolicy) => {
    if (policy === restart.policy) return;
    const needsWait = isServicePolicy(policy) && !(restart.waitSec ?? 0);
    onChange({ policy, ...(needsWait && { waitSec: DEFAULT_WAIT_SEC }) });
  };
  return (
    <>
      <p className="text-sm text-muted-foreground">
        {ts(
          'The scheduler keeps one run of this workflow alive. It starts a new run when the last run ends.'
        )}
      </p>
      {conflict && (
        <p
          role="note"
          className="rounded-md border border-warning px-2 py-1.5 text-xs text-foreground"
        >
          {conflict}
        </p>
      )}
      <div
        role="radiogroup"
        aria-label={ts('Restart policy')}
        className="flex gap-1 rounded-md border p-0.5"
      >
        {POLICIES.map((policy) => {
          const checked = restart.policy === policy;
          return (
            <button
              key={policy}
              type="button"
              role="radio"
              aria-checked={checked}
              disabled={
                !editable || (!on && policy !== 'no' && conflict !== null)
              }
              onClick={() => choose(policy)}
              className={cn(
                'h-7 flex-1 rounded px-2 text-xs transition-colors disabled:opacity-50',
                checked
                  ? 'bg-primary text-primary-foreground'
                  : 'text-foreground hover:bg-accent'
              )}
            >
              {ts(POLICY_LABELS[policy])}
            </button>
          );
        })}
      </div>
      {restart.policy !== 'no' && (
        <>
          <p className="text-xs text-muted-foreground">
            {ts(POLICY_HINTS[restart.policy])}
          </p>
          <section className="space-y-2" aria-label={ts('Restart settings')}>
            <CountField
              key={`wait-${restart.waitSec}`}
              label={ts('Wait before a new run (s)')}
              value={restart.waitSec}
              editable={editable}
              optional={false}
              onCommit={(waitSec) => onChange({ waitSec })}
            />
            <CountField
              key={`runs-${restart.histRetentionRuns}`}
              label={ts('Runs kept in the history')}
              value={restart.histRetentionRuns}
              placeholder="100"
              editable={editable}
              optional
              onCommit={(histRetentionRuns) => onChange({ histRetentionRuns })}
            />
            <p className="text-xs text-muted-foreground">
              {ts('With no value, the history keeps the latest 100 runs.')}
            </p>
          </section>
          {editable && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="text-destructive"
              onClick={() => onChange({ policy: 'no' })}
            >
              <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
              {ts('Remove the restart policy')}
            </Button>
          )}
        </>
      )}
      {onSwitch && editable && (
        <SwitchButton label="Switch to Cron" onClick={onSwitch} />
      )}
    </>
  );
}

export function AlwaysOnLauncherPanel({
  onClose,
  ...props
}: AlwaysProps & { onClose(): void }): ReactElement {
  return (
    <Shell id="always" onClose={onClose}>
      <AlwaysBody {...props} />
    </Shell>
  );
}

/** The action at the bottom of a form of the slot: it removes the setting and offers the other. */
function SwitchButton({
  label,
  onClick,
}: {
  label: string;
  onClick(): void;
}): ReactElement {
  const { ts } = useI18n();
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className="self-start"
      onClick={onClick}
    >
      <ArrowLeftRight className="h-3.5 w-3.5" aria-hidden="true" />
      {ts(label)}
    </Button>
  );
}

type SlotChoice = 'cron' | 'always';

const SLOT_CHOICES: ReadonlyArray<{
  id: SlotChoice;
  label: string;
  hint: string;
}> = [
  {
    id: 'cron',
    label: 'Cron',
    hint: 'Start on a schedule of cron expressions.',
  },
  {
    id: 'always',
    label: 'Always on',
    hint: 'Keep one run alive and start a new one when it ends.',
  },
];

/**
 * The panel of the slot that Cron and Always on share. With neither set, it
 * has a chooser: the choice opens a form and writes nothing until the author
 * sets a value. With one set, it shows that form and an action that removes
 * the setting and goes back to the chooser. A file with both (invalid) shows
 * the form of the open node, with the same action on each.
 */
export function SlotLauncherPanel({
  open,
  schedule,
  restart,
  editable,
  upcomingRuns,
  upcomingRunsHold,
  schedules,
  onSchedule,
  onRestart,
  onClose,
}: {
  /** The node of the slot that opened the panel. */
  open: 'schedule' | 'cron' | 'always';
  schedule: ScheduleView;
  restart: RestartView;
  editable: boolean;
  upcomingRuns?: UpcomingRun[];
  upcomingRunsHold?: UpcomingRunsHold;
  schedules?: Schedule[];
  /** The new list of start expressions. */
  onSchedule(expressions: string[]): void;
  onRestart: AlwaysProps['onChange'];
  onClose(): void;
}): ReactElement {
  const { ts } = useI18n();
  const [choice, setChoice] = useState<SlotChoice | null>(null);
  const scheduled = schedule.present;
  const service = isServicePolicy(restart.policy);
  const both = scheduled && service;
  const shown: SlotChoice | null = both
    ? open === 'cron'
      ? 'cron'
      : 'always'
    : scheduled
      ? 'cron'
      : service
        ? 'always'
        : choice;
  // The chooser shows while neither is set.
  const choosing = !scheduled && !service;
  return (
    <Shell id={open} onClose={onClose}>
      {both && (
        <p
          role="note"
          className="rounded-md border border-warning px-2 py-1.5 text-xs text-foreground"
        >
          {ts(
            'A service DAG cannot have a schedule or a retry_policy. Remove it first.'
          )}
        </p>
      )}
      {choosing && (
        <div
          role="radiogroup"
          aria-label={ts('Choose how the workflow starts')}
          className="space-y-1.5"
        >
          {SLOT_CHOICES.map((option) => {
            const Icon = LAUNCHER_ICONS[option.id];
            return (
              <button
                key={option.id}
                type="button"
                role="radio"
                aria-checked={choice === option.id}
                onClick={() => setChoice(option.id)}
                className={cn(
                  'flex w-full items-start gap-2 rounded-md border px-2.5 py-2 text-left transition-colors hover:bg-accent',
                  choice === option.id && 'border-primary bg-accent'
                )}
              >
                <Icon
                  className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground"
                  aria-hidden="true"
                />
                <span className="min-w-0">
                  <span className="block text-sm font-medium text-foreground">
                    {ts(option.label)}
                  </span>
                  <span className="block text-xs text-muted-foreground">
                    {ts(option.hint)}
                  </span>
                </span>
              </button>
            );
          })}
        </div>
      )}
      {shown === 'cron' && (
        <CronBody
          schedule={schedule}
          editable={editable}
          upcomingRuns={upcomingRuns}
          upcomingRunsHold={upcomingRunsHold}
          schedules={schedules}
          alwaysOn={service}
          onChange={onSchedule}
          onSwitch={
            scheduled
              ? () => {
                  setChoice('always');
                  onSchedule([]);
                }
              : undefined
          }
        />
      )}
      {shown === 'always' && (
        <AlwaysBody
          restart={restart}
          editable={editable}
          onChange={onRestart}
          onSwitch={
            service
              ? () => {
                  setChoice('cron');
                  onRestart({ policy: 'no' });
                }
              : undefined
          }
        />
      )}
    </Shell>
  );
}

/** The webhook of the workflow: the same body as the old Webhook settings. */
export function WebhookLauncherPanel({
  fileName,
  dagName,
  onSecretVisibleChange,
  onClose,
}: {
  fileName: string;
  dagName: string;
  onSecretVisibleChange?(visible: boolean): void;
  onClose(): void;
}): ReactElement {
  return (
    <Shell id="webhook" onClose={onClose}>
      <WebhookTab
        fileName={fileName}
        dagName={dagName}
        onSecretVisibleChange={onSecretVisibleChange}
      />
    </Shell>
  );
}
