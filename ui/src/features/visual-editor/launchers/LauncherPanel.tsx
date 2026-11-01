// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus, Trash2 } from 'lucide-react';
import { useState, type ReactElement, type ReactNode } from 'react';
import type { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { useConfig } from '@/contexts/ConfigContext';
import WebhookTab from '@/features/dags/components/dag-details/WebhookTab';
import {
  PANEL_BODY_CLASS,
  PanelHeader,
} from '@/features/dags/components/step-details/PanelShell';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import type { ScheduleView } from '../doc';
import { LAUNCHER_ICONS, LAUNCHER_LABELS } from './LauncherNode';
import type { LauncherId } from './model';
import { nextRuns, SCHEDULE_PRESETS } from './nextRuns';
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
  return (
    <div
      data-testid={`ve-launcher-panel-${id}`}
      className="flex min-h-0 flex-1 flex-col"
    >
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
  const { tz } = useConfig();
  const [draft, setDraft] = useState(expression);
  const runs = nextRuns(draft.trim(), 3, new Date(), tz);
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
            aria-label={ts('Remove this expression')}
            title={ts('Remove this expression')}
            onClick={onRemove}
          >
            <Trash2 className="h-4 w-4" aria-hidden="true" />
          </Button>
        )}
      </div>
      {!previewed ? null : runs ? (
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
      ) : (
        <p className="text-xs text-muted-foreground">
          {ts('No preview for this expression')}
        </p>
      )}
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
  schedules = [],
  onChange,
  onClose,
}: {
  schedule: ScheduleView;
  editable: boolean;
  /** Next runs of the saved or validated schedule, from the server. */
  upcomingRuns?: UpcomingRun[];
  /** The server's start schedules, which upcomingRuns index. */
  schedules?: Schedule[];
  /** The new list of start expressions; the other entries stay. */
  onChange(expressions: string[]): void;
  onClose(): void;
}): ReactElement {
  const { ts } = useI18n();
  const [added, setAdded] = useState('');
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
              key={`${expression}-${index}`}
              expression={expression}
              editable={editable}
              serverPreview={upcomingRuns !== undefined}
              onCommit={(next) => {
                if (expressions.includes(next)) return false;
                onChange(expressions.map((e, i) => (i === index ? next : e)));
                return true;
              }}
              onRemove={() =>
                onChange(expressions.filter((_, i) => i !== index))
              }
            />
          ))}
        </ul>
      )}
      {upcomingRuns && schedule.present && (
        <UpcomingRuns runs={upcomingRuns} schedules={schedules} />
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
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="text-destructive"
          onClick={() => onChange([])}
        >
          <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
          {ts('Remove schedule')}
        </Button>
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
