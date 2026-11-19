// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Gauge } from 'lucide-react';
import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { DagEditOp, StepView, ValidationIssue } from '../contracts';
import { FieldIssues } from '../inspector/FieldIssues';
import { issuesUnder } from '../inspector/issues';
import { AgentRunLimits } from '../inspector/sections/agentRun/AgentRunLimits';
import { LimitField } from '../inspector/sections/tools/ToolsSection';
import { limitFacts, type LimitFact } from '../tools/dockModel';
import type { ToolStepKind } from '../tools/kinds';
import {
  DEFAULT_MAX_TOOLS,
  DEFAULT_TOOL_RESULT_MAX_BYTES,
} from '../tools/model';
import { readStepTools } from '../tools/parse';
import { DetailHeading } from './DockParts';

// The limit fields take numbers: no reference is offered.
const NO_CANDIDATES: never[] = [];

interface LimitsProps {
  step: StepView;
  stepKind: ToolStepKind;
  readOnly: boolean;
  fieldIssues: Record<string, ValidationIssue[]>;
  onEdit(op: DagEditOp): void;
}

/** The limits as small figures: what applies now, with its default. */
export function LimitFigures({
  facts,
  className,
}: {
  facts: readonly LimitFact[];
  className?: string;
}): ReactElement {
  return (
    <dl
      data-testid="ve-dock-limit-figures"
      className={cn('flex flex-wrap gap-x-4 gap-y-1', className)}
    >
      {facts.map((fact) => (
        <div
          key={fact.id}
          data-limit={fact.id}
          data-default={fact.isDefault || undefined}
          className="flex items-baseline gap-1.5"
        >
          <dt className="text-[11px] text-muted-foreground">{fact.label}</dt>
          <dd
            className={cn(
              'text-xs font-medium',
              fact.isDefault && 'text-muted-foreground'
            )}
          >
            {fact.value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * The limits of the tools of a step, as one block with what they do. An agent
 * step has the turns of the CLI, the calls of the tool bridge and the timeout
 * of the step. A chat step has the size of the catalog and the size of one tool
 * result. Each field has the default as its placeholder. The edits are the
 * edits of the step form.
 */
export function LimitsDetail({
  step,
  stepKind,
  readOnly,
  fieldIssues,
  onEdit,
}: LimitsProps): ReactElement {
  const { ts } = useI18n();
  const facts = limitFacts(ts, stepKind, step.value);
  const limits = readStepTools(step.value);
  const setLimit =
    (key: 'max_tools' | 'tool_result_max_bytes') =>
    (value: number | undefined) =>
      onEdit({
        kind: 'editStepTools',
        step: step.key,
        change: { kind: 'setLimit', key, value },
      });
  return (
    <div
      data-testid="ve-dock-limits"
      data-field-path="limits"
      className="space-y-3 p-4"
    >
      <header className="flex items-start gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
          <Gauge className="size-5" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="text-sm font-semibold leading-5">{ts('Limits')}</h2>
          <p className="text-xs text-muted-foreground">
            {stepKind === 'agent'
              ? ts(
                  'Limits stop a run that loops or calls too much. An empty field uses the default.'
                )
              : ts(
                  'Limits keep the catalog and the results small. An empty field uses the default.'
                )}
          </p>
        </div>
      </header>
      <LimitFigures
        facts={facts}
        className="rounded-md border border-border px-3 py-2"
      />
      <section>
        <DetailHeading>{ts('Edit the limits')}</DetailHeading>
        {stepKind === 'agent' ? (
          <AgentRunLimits
            step={step}
            readOnly={readOnly}
            onEdit={onEdit}
            fieldIssues={fieldIssues}
            candidates={NO_CANDIDATES}
          />
        ) : (
          <div className="space-y-3">
            <div data-field-path="with.max_tools">
              <LimitField
                label={ts('Maximum number of tools')}
                value={limits.maxTools}
                fallback={DEFAULT_MAX_TOOLS}
                hint={ts('Enter a whole number')}
                readOnly={readOnly}
                onSet={setLimit('max_tools')}
              />
              <p className="mt-0.5 text-[11px] text-muted-foreground">
                {ts('A catalog with more tools fails the step when it starts.')}
              </p>
              <FieldIssues
                issues={issuesUnder(fieldIssues, 'with.max_tools')}
              />
            </div>
            <div data-field-path="with.tool_result_max_bytes">
              <LimitField
                label={ts(
                  'Maximum size of a tool result, in bytes (0: no limit)'
                )}
                value={limits.resultMaxBytes}
                fallback={DEFAULT_TOOL_RESULT_MAX_BYTES}
                readOnly={readOnly}
                onSet={setLimit('tool_result_max_bytes')}
              />
              <p className="mt-0.5 text-[11px] text-muted-foreground">
                {ts('A longer result is cut before the model gets it.')}
              </p>
              <FieldIssues
                issues={issuesUnder(fieldIssues, 'with.tool_result_max_bytes')}
              />
            </div>
          </div>
        )}
      </section>
    </div>
  );
}
