// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AlertTriangle, Plus, SlidersHorizontal, Trash2 } from 'lucide-react';
import { useState, type ReactElement, type ReactNode } from 'react';
import {
  ScheduleKind,
  type components,
  type UpcomingRunsHold,
} from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/ui/icon-button';
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
import type { ValidationIssue } from '../contracts';
import {
  scheduleParamIssueIndex,
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
export function CronLauncherPanel({
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
  onClose,
}: {
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
  onClose(): void;
}): ReactElement {
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
    <Shell id="cron" onClose={onClose}>
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
