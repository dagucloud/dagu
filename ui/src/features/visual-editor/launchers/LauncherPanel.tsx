// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  AlertTriangle,
  ArrowLeftRight,
  Plus,
  SlidersHorizontal,
  Trash2,
} from 'lucide-react';
import {
  useEffect,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react';
import {
  ScheduleKind,
  type components,
  type UpcomingRunsHold,
} from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/ui/icon-button';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { useConfig } from '@/contexts/ConfigContext';
import WebhookTab from '@/features/dags/components/dag-details/WebhookTab';
import {
  PANEL_BODY_CLASS,
  PanelHeader,
  usePanelColumn,
} from '@/features/dags/components/step-details/PanelShell';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import {
  describeScheduleCoincidence,
  getScheduleSummary,
} from '@/lib/dagSchedule';
import { cn } from '@/lib/utils';
import type { ValidationIssue } from '../contracts';
import {
  isServicePolicy,
  scheduleParamIssueIndex,
  type RestartPolicy,
  type RestartSettings,
  type ScheduleEntryView,
  type ScheduleView,
} from '../doc';
import { FieldIssues } from '../inspector/FieldIssues';
import { LAUNCHER_ICONS, LAUNCHER_LABELS } from './LauncherNode';
import type { LauncherId } from './model';
import { nextRuns, SCHEDULE_PRESETS } from './nextRuns';
import { ParamOverrideFields, type OverrideValue } from './ParamOverrideFields';
import { ScheduleBuilder, ScheduleSummary } from './ScheduleBuilder';
import { UpcomingRuns } from './UpcomingRuns';

type ParamDef = components['schemas']['ParamDef'];
type Schedule = components['schemas']['Schedule'];
type ScheduleCoincidence = components['schemas']['ScheduleCoincidence'];
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

/** Schedule rows that the panel lists, with the server issues of each. */
interface RowContext {
  editable: boolean;
  paramDefs: readonly ParamDef[];
  /** Issues of the row's overrides, from the server. */
  issues: ValidationIssue[];
  /** Why the entry starts no run at a time it shares with another one. */
  coincidence?: string;
  onParamChange(name: string, value: OverrideValue | null): void;
}

/** The message shown when an expression is refused as already listed. */
const DUPLICATE_EXPRESSION = 'This expression is already in the schedule.';

function ExpressionRow({
  expression,
  params,
  serverPreview,
  onCommit,
  onRemove,
  ...row
}: RowContext & {
  expression: string;
  params: Record<string, string>;
  /** The panel lists the server's runs, so the row previews only drafts. */
  serverPreview: boolean;
  /** False when the panel refuses the expression: the draft goes back. */
  onCommit(next: string): boolean;
  onRemove(): void;
}): ReactElement {
  const { ts } = useI18n();
  const { editable } = row;
  const [draft, setDraft] = useState(expression);
  const [formOpen, setFormOpen] = useState(false);
  const [duplicate, setDuplicate] = useState(false);
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
    const refused = next !== '' && !onCommit(next);
    setDuplicate(refused);
    if (next === '' || refused) setDraft(expression);
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
          onChange={(event) => {
            setDraft(event.target.value);
            setDuplicate(false);
          }}
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
          <IconButton
            icon={Trash2}
            tone="destructive"
            label={ts('Remove schedule')}
            onClick={onRemove}
          />
        )}
      </div>
      {duplicate && (
        <p role="alert" className="text-xs text-destructive">
          {ts(DUPLICATE_EXPRESSION)}
        </p>
      )}
      {editable && formOpen && (
        <ScheduleBuilder
          expression={draft}
          onChange={(next) => {
            const accepted = next === expression || onCommit(next);
            setDuplicate(!accepted);
            if (accepted) setDraft(next);
          }}
        />
      )}
      <ScheduleSummary expression={draft} />
      <CoincidenceWarning text={row.coincidence} />
      <RowParams params={params} {...row} />
      {previewed && <NextRunsPreview expression={draft.trim()} />}
    </li>
  );
}

/**
 * An entry that the panel does not edit as cron, such as a one-off or an
 * entry with a calendar or a profile. Its overrides stay editable.
 */
function EntryRow({
  entry,
  ...row
}: RowContext & { entry: ScheduleEntryView }): ReactElement {
  const { ts } = useI18n();
  return (
    <li className="space-y-1">
      <p className="break-all rounded-md border bg-muted px-2 py-1 font-mono text-sm text-foreground">
        {getScheduleSummary(entry.schedule)}
      </p>
      <p className="text-xs text-muted-foreground">
        {ts(
          entry.params === null
            ? 'Edit this entry and its parameters in the YAML.'
            : 'Edit the timing of this entry in the YAML.'
        )}
      </p>
      <CoincidenceWarning text={row.coincidence} />
      {entry.params !== null && <RowParams params={entry.params} {...row} />}
    </li>
  );
}

function CoincidenceWarning({ text }: { text?: string }): ReactElement | null {
  if (!text) return null;
  return (
    <p className="flex items-start gap-1 text-xs text-warning">
      <AlertTriangle className="mt-0.5 h-3 w-3 shrink-0" aria-hidden="true" />
      <span>{text}</span>
    </p>
  );
}

function RowParams({
  params,
  editable,
  paramDefs,
  issues,
  onParamChange,
}: RowContext & { params: Record<string, string> }): ReactElement {
  return (
    <>
      <ParamOverrideFields
        defs={paramDefs}
        values={params}
        editable={editable}
        onChange={onParamChange}
      />
      <FieldIssues issues={issues} />
    </>
  );
}

/**
 * Whether a server schedule still describes a start entry as the text writes
 * it: the same timing and profile. A server schedule may take the calendar
 * of the DAG, so a calendar is compared only when the entry names one.
 */
function describesEntry(
  server: Schedule | undefined,
  { schedule }: ScheduleEntryView
): boolean {
  if (!server) return false;
  const sameExpression =
    (server.expression ?? '').trim() === (schedule.expression ?? '').trim();
  const sameTiming =
    schedule.kind === ScheduleKind.at
      ? server.kind === ScheduleKind.at &&
        Date.parse(server.at ?? '') === Date.parse(schedule.at ?? '')
      : server.kind !== ScheduleKind.at && sameExpression;
  return (
    sameTiming &&
    (server.profile ?? '') === (schedule.profile ?? '') &&
    (schedule.calendar === undefined ||
      server.calendar === schedule.calendar) &&
    (server.businessDays ?? []).join() === (schedule.businessDays ?? []).join()
  );
}

/**
 * The start schedule of the workflow. Plain cron entries are edited here;
 * other entries, such as one-offs, show with their parameter overrides. An
 * edit is one change of the YAML text. The server's next runs, when given,
 * cover every start entry; without them each expression is previewed in the
 * browser.
 */
interface CronProps {
  schedule: ScheduleView;
  editable: boolean;
  /** Next runs of the saved or validated schedule, from the server. */
  upcomingRuns?: UpcomingRun[];
  /** Why none of the upcoming runs will start, if they will not. */
  upcomingRunsHold?: UpcomingRunsHold;
  /** The server's start schedules, which upcomingRuns index. */
  schedules?: Schedule[];
  /** Parameters of the workflow, whose defaults each schedule inherits. */
  paramDefs?: readonly ParamDef[];
  /** Issues of the validated workflow; those of schedule overrides show on their rows. */
  issues?: ValidationIssue[];
  /** Start schedules that another one starts in place of, indexing schedules. */
  coincidences?: ScheduleCoincidence[];
  /** The new list of start expressions; the other entries stay. */
  onChange(expressions: string[], removedIndex?: number): void;
  /** Sets (value) or clears (null) a parameter override of a start entry. */
  onParamChange?(
    entryIndex: number,
    name: string,
    value: OverrideValue | null
  ): void;
}

function CronBody({
  schedule,
  editable,
  upcomingRuns,
  upcomingRunsHold,
  schedules = [],
  paramDefs = [],
  issues = [],
  coincidences,
  onChange,
  onParamChange,
}: CronProps): ReactElement {
  const { ts, locale } = useI18n();
  const dayjsLocale = locale === 'zh-CN' ? 'zh-cn' : locale;
  const [added, setAdded] = useState('');
  // A row keeps its position as key so that it stays mounted across its
  // own edits. A removal moves the rows after it to other positions, so it
  // remounts the rows and their state does not pass to another expression.
  const [removals, setRemovals] = useState(0);
  const { expressions } = schedule;
  const addedDuplicate = expressions.includes(added.trim());
  const add = (expression: string) => {
    const next = expression.trim();
    if (next === '' || expressions.includes(next)) return;
    onChange([...expressions, next]);
    setAdded('');
  };
  // The server's schedules may lag behind the text being edited: they
  // describe the rows only when they list the same entries.
  const aligned =
    schedules.length === schedule.entries.length &&
    schedule.entries.every((entry, index) =>
      describesEntry(schedules[index], entry)
    );
  const coincidenceOf = (entryIndex: number) =>
    aligned
      ? describeScheduleCoincidence(
          entryIndex,
          coincidences,
          schedules,
          ts,
          dayjsLocale
        )
      : undefined;
  const issuesOf = (entryIndex: number) =>
    issues.filter(
      (issue) =>
        issue.fieldPath !== undefined &&
        scheduleParamIssueIndex(issue.fieldPath) === entryIndex
    );
  return (
    <>
      <p className="text-sm text-muted-foreground">
        {ts('The workflow starts on each of these schedules.')}
      </p>
      {schedule.entries.length === 0 ? (
        <p className="text-sm italic text-muted-foreground">
          {ts('No schedule defined')}
        </p>
      ) : (
        <ul className="space-y-3" aria-label={ts('Schedules')}>
          {schedule.entries.map((entry, entryIndex) => {
            const row: RowContext = {
              editable,
              paramDefs,
              issues: issuesOf(entryIndex),
              coincidence: coincidenceOf(entryIndex),
              onParamChange: (name, value) =>
                onParamChange?.(entryIndex, name, value),
            };
            const index = entry.expressionIndex;
            if (index === undefined) {
              return (
                <EntryRow
                  key={`${removals}-${entryIndex}`}
                  entry={entry}
                  {...row}
                />
              );
            }
            return (
              <ExpressionRow
                key={`${removals}-${entryIndex}`}
                expression={expressions[index] ?? ''}
                params={entry.params ?? {}}
                serverPreview={upcomingRuns !== undefined}
                onCommit={(next) => {
                  if (expressions.includes(next)) return false;
                  onChange(expressions.map((e, i) => (i === index ? next : e)));
                  return true;
                }}
                onRemove={() => {
                  setRemovals((count) => count + 1);
                  onChange(
                    expressions.filter((_, i) => i !== index),
                    index
                  );
                }}
                {...row}
              />
            );
          })}
        </ul>
      )}
      {upcomingRuns && schedule.present && (
        <UpcomingRuns
          runs={upcomingRuns}
          hold={upcomingRunsHold}
          schedules={schedules}
        />
      )}
      {editable && (
        <section className="space-y-2" aria-label={ts('Add an expression')}>
          <ScheduleBuilder expression={added} onChange={setAdded} />
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={added}
              spellCheck={false}
              placeholder="0 * * * *"
              aria-label={ts('New cron expression')}
              aria-invalid={addedDuplicate || undefined}
              onChange={(event) => setAdded(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
                  add(added);
                }
              }}
              className="h-8 min-w-0 flex-1 rounded-md border bg-background px-2 font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            />
            <IconButton
              icon={Plus}
              label={ts('Add schedule')}
              disabled={added.trim() === '' || addedDuplicate}
              onClick={() => add(added)}
            />
          </div>
          {addedDuplicate && (
            <p role="alert" className="text-xs text-destructive">
              {ts(DUPLICATE_EXPRESSION)}
            </p>
          )}
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
  // The field stays mounted when its value changes, so that it keeps the
  // focus. A new value replaces the draft.
  const [shown, setShown] = useState(saved);
  if (shown !== saved) {
    setShown(saved);
    setDraft(saved);
  }
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
  restart: RestartSettings;
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
}: AlwaysProps): ReactElement {
  const { ts } = useI18n();
  const on = isServicePolicy(restart.policy);
  const conflict = restart.retryPolicy
    ? ts(
        'Remove the retry_policy of the workflow first: a workflow that is always on cannot have one.'
      )
    : null;
  const choose = (policy: RestartPolicy) => {
    if (policy === restart.policy) return;
    // The loader refuses a service DAG with no wait, and the base
    // configuration is not known here.
    const needsWait = isServicePolicy(policy) && !(restart.waitSec ?? 0);
    onChange({ policy, ...(needsWait && { waitSec: DEFAULT_WAIT_SEC }) });
  };
  // The 100 runs are the default only while the file sets no days.
  const defaultRuns = restart.histRetentionDays === null;
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
      <ToggleGroup aria-label={ts('Restart policy')} className="flex">
        {POLICIES.map((policy) => (
          <ToggleButton
            key={policy}
            value={policy}
            groupValue={restart.policy}
            disabled={
              !editable || (!on && policy !== 'no' && conflict !== null)
            }
            onClick={() => choose(policy)}
            className="flex-1"
          >
            {ts(POLICY_LABELS[policy])}
          </ToggleButton>
        ))}
      </ToggleGroup>
      {restart.policy !== 'no' && (
        <>
          <p className="text-xs text-muted-foreground">
            {ts(POLICY_HINTS[restart.policy])}
          </p>
          <section className="space-y-2" aria-label={ts('Restart settings')}>
            <CountField
              label={ts('Wait before a new run (s)')}
              value={restart.waitSec}
              editable={editable}
              optional={false}
              onCommit={(waitSec) => onChange({ waitSec })}
            />
            <CountField
              label={ts('Runs kept in the history')}
              value={restart.histRetentionRuns}
              placeholder={defaultRuns ? '100' : undefined}
              editable={editable}
              optional
              onCommit={(histRetentionRuns) => onChange({ histRetentionRuns })}
            />
            {defaultRuns && (
              <p className="text-xs text-muted-foreground">
                {ts('With no value, the history keeps the latest 100 runs.')}
              </p>
            )}
          </section>
        </>
      )}
    </>
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
    <Button type="button" variant="outline" size="sm" onClick={onClick}>
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

/** The first control of a form that can take the focus. */
const FIRST_CONTROL =
  'button:not([disabled]), input:not([readonly]), select, textarea';

/**
 * The panel of the slot that Cron and Always on share. With neither set, it
 * has a chooser: the choice opens a form and writes nothing until the author
 * sets a value. With one set, it shows that form and an action that removes
 * the setting and goes back to the chooser, with the focus in the other
 * form. A schedule that the action cannot remove whole has a note in its
 * place. A file with both (invalid) shows the form of the open node, with
 * the same action on each.
 */
export function SlotLauncherPanel({
  open,
  schedule,
  scheduleRemovable,
  restart,
  editable,
  upcomingRuns,
  upcomingRunsHold,
  schedules,
  paramDefs,
  issues,
  coincidences,
  onSchedule,
  onScheduleParam,
  onRestart,
  onClose,
}: {
  /** The node of the slot that opened the panel. */
  open: 'schedule' | 'cron' | 'always';
  schedule: ScheduleView;
  /**
   * True when the removal of the start expressions leaves no schedule entry:
   * a restart policy cannot go with what is left of one.
   */
  scheduleRemovable: boolean;
  restart: RestartSettings;
  editable: boolean;
  upcomingRuns?: UpcomingRun[];
  upcomingRunsHold?: UpcomingRunsHold;
  schedules?: Schedule[];
  /** Parameters of the workflow, whose defaults each schedule inherits. */
  paramDefs?: readonly ParamDef[];
  /** Issues of the validated workflow; those of schedule overrides show on their rows. */
  issues?: ValidationIssue[];
  /** Start schedules that another one starts in place of, indexing schedules. */
  coincidences?: ScheduleCoincidence[];
  /** The new list of start expressions, and the index of a removed one. */
  onSchedule(expressions: string[], removedIndex?: number): void;
  /** Sets (value) or clears (null) a parameter override of an expression. */
  onScheduleParam?(
    entryIndex: number,
    name: string,
    value: OverrideValue | null
  ): void;
  onRestart: AlwaysProps['onChange'];
  onClose(): void;
}): ReactElement {
  const { ts } = useI18n();
  const [choice, setChoice] = useState<SlotChoice | null>(null);
  const scheduled = restart.schedule;
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
  const formRef = useRef<HTMLDivElement>(null);
  // The form that takes the focus when it shows: the other one, after a switch.
  const focusNext = useRef<SlotChoice | null>(null);
  useEffect(() => {
    if (focusNext.current === shown) {
      formRef.current?.querySelector<HTMLElement>(FIRST_CONTROL)?.focus();
      focusNext.current = null;
    }
    // A setting is still in the file: the switch is over, or was refused.
    if (!choosing) focusNext.current = null;
  });
  const switchTo = (next: SlotChoice) => {
    focusNext.current = next;
    setChoice(next);
    if (next === 'always') onSchedule([]);
    // The wait belongs to the policy. The runs kept also apply to a schedule.
    else onRestart({ policy: 'no', waitSec: null });
  };
  // Off keeps the Always on form open: without a choice, only the chooser
  // would show.
  const changeRestart: typeof onRestart = (change) => {
    if (change.policy === 'no') setChoice('always');
    onRestart(change);
  };
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
        <ToggleGroup
          aria-label={ts('Choose how the workflow starts')}
          className="flex flex-col gap-1.5 bg-transparent p-0"
        >
          {SLOT_CHOICES.map((option) => {
            const Icon = LAUNCHER_ICONS[option.id];
            return (
              <ToggleButton
                key={option.id}
                value={option.id}
                groupValue={choice ?? undefined}
                onClick={() => setChoice(option.id)}
                className={cn(
                  'h-auto w-full items-start justify-start gap-2 border px-2.5 py-2 text-left hover:bg-accent',
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
                  <span className="block text-xs font-normal text-muted-foreground">
                    {ts(option.hint)}
                  </span>
                </span>
              </ToggleButton>
            );
          })}
        </ToggleGroup>
      )}
      {shown !== null && (
        <div ref={formRef} className="space-y-4">
          {shown === 'cron' ? (
            <CronBody
              schedule={schedule}
              editable={editable}
              upcomingRuns={upcomingRuns}
              upcomingRunsHold={upcomingRunsHold}
              schedules={schedules}
              paramDefs={paramDefs}
              issues={issues}
              coincidences={coincidences}
              onChange={onSchedule}
              onParamChange={onScheduleParam}
            />
          ) : (
            <AlwaysBody
              restart={restart}
              editable={editable}
              onChange={changeRestart}
            />
          )}
          {editable &&
            shown === 'cron' &&
            scheduled &&
            (restart.retryPolicy ? (
              <p className="text-xs text-muted-foreground">
                {ts(
                  'Remove the retry_policy of the workflow first: a workflow that is always on cannot have one.'
                )}
              </p>
            ) : scheduleRemovable ? (
              <SwitchButton
                label="Switch to Always on"
                onClick={() => switchTo('always')}
              />
            ) : (
              <p className="text-xs text-muted-foreground">
                {ts('Edit the schedule in the YAML to switch to Always on.')}
              </p>
            ))}
          {editable && shown === 'always' && service && (
            <SwitchButton
              label="Switch to Cron"
              onClick={() => switchTo('cron')}
            />
          )}
        </div>
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
