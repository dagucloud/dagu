// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus, RotateCcw, SlidersHorizontal, Trash2 } from 'lucide-react';
import { useState, type ReactElement, type ReactNode } from 'react';
import type { components, UpcomingRunsHold } from '@/api/v1/schema';
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
import type { ScheduleView } from '../doc';
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

/** A named parameter of the workflow and its default, for the schedule fields. */
export interface ParamDefault {
  name: string;
  default?: string;
}

/**
 * One field per workflow parameter. The workflow default shows as the muted
 * placeholder; a typed value is an override of this schedule, and the reset
 * button clears it. An emptied field has no override.
 */
function ParamOverrideFields({
  defaults,
  values,
  editable,
  onChange,
}: {
  defaults: readonly ParamDefault[];
  values: Record<string, string>;
  editable: boolean;
  onChange(name: string, value: string | null): void;
}): ReactElement | null {
  const { ts } = useI18n();
  if (defaults.length === 0) return null;
  const overrides = new Map(Object.entries(values));
  return (
    <div className="space-y-1" role="group" aria-label={ts('Parameters')}>
      {defaults.map((param) => {
        const overridden = overrides.has(param.name);
        return (
          <div key={param.name} className="flex items-center gap-2">
            <label className="flex min-w-0 flex-1 items-center gap-2 text-xs font-medium text-muted-foreground">
              <span className="w-20 shrink-0 truncate" title={param.name}>
                {param.name}
              </span>
              <input
                type="text"
                value={overrides.get(param.name) ?? ''}
                placeholder={param.default ?? ''}
                readOnly={!editable}
                spellCheck={false}
                aria-label={param.name}
                onChange={(event) =>
                  onChange(
                    param.name,
                    event.target.value === '' ? null : event.target.value
                  )
                }
                className="h-7 min-w-0 flex-1 rounded-md border bg-background px-2 font-mono text-xs text-foreground outline-none placeholder:text-muted-foreground/70 focus-visible:ring-2 focus-visible:ring-ring"
              />
            </label>
            {editable && (
              <span className="flex h-7 w-16 shrink-0 items-center">
                {overridden && (
                  <IconButton
                    size="sm"
                    icon={RotateCcw}
                    label={ts('Use workflow default for {name}', {
                      name: param.name,
                    })}
                    onClick={() => onChange(param.name, null)}
                  />
                )}
              </span>
            )}
          </div>
        );
      })}
    </div>
  );
}

function ExpressionRow({
  expression,
  editable,
  paramDefaults,
  paramValues,
  serverPreview,
  onCommit,
  onParamChange,
  onRemove,
}: {
  expression: string;
  editable: boolean;
  paramDefaults: readonly ParamDefault[];
  paramValues: Record<string, string>;
  onParamChange(name: string, value: string | null): void;
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
          <IconButton
            icon={Trash2}
            tone="destructive"
            label={ts('Remove schedule')}
            onClick={onRemove}
          />
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
      <ParamOverrideFields
        defaults={paramDefaults}
        values={paramValues}
        editable={editable}
        onChange={onParamChange}
      />
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
export function CronLauncherPanel({
  schedule,
  editable,
  upcomingRuns,
  upcomingRunsHold,
  schedules = [],
  paramDefaults = [],
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
  /** Named parameters of the workflow, whose defaults each schedule inherits. */
  paramDefaults?: readonly ParamDefault[];
  /** The new list of start expressions; the other entries stay. */
  onChange(expressions: string[], removedIndex?: number): void;
  /** Sets (value) or clears (null) a parameter override of an expression. */
  onParamChange?(index: number, name: string, value: string | null): void;
  onClose(): void;
}): ReactElement {
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
    <Shell id="cron" onClose={onClose}>
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
              paramDefaults={paramDefaults}
              paramValues={schedule.params?.[index] ?? {}}
              onParamChange={(name, value) =>
                onParamChange?.(index, name, value)
              }
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
            />
          ))}
        </ul>
      )}
      {upcomingRuns && schedule.present && (
        <UpcomingRuns
          runs={upcomingRuns}
          hold={upcomingRunsHold}
          schedules={schedules}
          overrides={schedule.startParams}
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
              disabled={added.trim() === ''}
              onClick={() => add(added)}
            />
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
