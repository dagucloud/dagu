// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ArrowLeft, BookOpen, Workflow, Wrench } from 'lucide-react';
import { useMemo, type ReactElement } from 'react';
import { NoticeLine } from '@/features/dags/components/dag-editor/NoticeLine';
import { PanelHeader } from '@/features/dags/components/step-details/PanelShell';
import type { DagIdentity } from '@/features/ai-endpoints/useEndpointChoices';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { isAgentRunValue } from './agentRun/agentRun';
import type {
  DagDocModel,
  DagEditOp,
  StepView,
  ToolSelection,
  ValidationIssue,
} from './contracts';
import { issuesUnder, stepIssues } from './inspector/issues';
import type { ToolPick } from './tools/pick';
import { SkillsDock } from './toolsDock/SkillsDock';
import { ToolsDock } from './toolsDock/ToolsDock';

export interface SlotPanelProps {
  model: DagDocModel;
  /** The chat step or the agent step that holds the list. */
  step: StepView;
  /** The name of the step, for the way back to it. */
  stepLabel: string;
  /**
   * The list that the panel edits: `with.dags`, `with.tools` (the MCP servers
   * and the built-in tools) or `with.skills`.
   */
  slot: 'tools' | 'dags' | 'skills';
  /**
   * The dock opened from an Add button: the catalogue of what the step can add
   * shows first and the search takes the focus.
   */
  adding?: boolean;
  /** The issues of the step. */
  issues: ValidationIssue[];
  readOnly: boolean;
  /** The DAG that holds the step: it is no tool of itself, and the MCP server entries that it may use depend on it. */
  dag?: DagIdentity | undefined;
  onEdit(op: DagEditOp): void;
  /** Selects a tool entry: its panel replaces this one. */
  onOpenTool(tool: ToolSelection): void;
  /** Opens the enlarged view of the DAG of a DAG tool: it replaces this panel. */
  onOpenDag?(tool: ToolSelection): void;
  /** Adds a tool that the user picks: the panel of the new tool replaces this one. */
  onPickTool(pick: ToolPick): void;
  /** Shows the step itself: its form. */
  onBack(): void;
  onClose(): void;
}

/**
 * The right-hand dock of the DAGs card, of the Tools card or of the Skills
 * card of a chat step or an agent step, and the dock of the Add buttons of
 * the three. The DAGs dock and the Tools dock (`ToolsDock`) list the tools of
 * their own kinds and the catalogue of what the step can add, with a search
 * box, a detail area and the limits: the DAGs dock has the DAG tools, the
 * Tools dock has the MCP servers and the built-in tools. The Skills dock
 * (`SkillsDock`) has the same parts for the skills. This panel holds the title
 * bar, the way back to the step and the notice of a step with YAML errors.
 * Every change is an operation on the YAML, which stays the one source. The
 * dock is read-only when `readOnly` is set or the YAML has syntax errors. The
 * search box takes the focus when the dock opens.
 */
export function SlotPanel({
  model,
  step,
  stepLabel,
  slot,
  adding = false,
  issues,
  readOnly,
  dag,
  onEdit,
  onOpenTool,
  onOpenDag,
  onPickTool,
  onBack,
  onClose,
}: SlotPanelProps): ReactElement {
  const { ts } = useI18n();
  const grouped = useMemo(
    () => stepIssues(issues, step, ts),
    [issues, step, ts]
  );
  const fieldIssues = grouped.byField;
  const locked = readOnly || !model.ok || step.broken !== undefined;
  const skills = slot === 'skills';
  const agent = isAgentRunValue(step.value, step.action);
  const total = issuesUnder(fieldIssues, `with.${slot}`).length;
  const Icon = skills ? BookOpen : slot === 'dags' ? Workflow : Wrench;
  return (
    <div
      data-testid="ve-slot-panel"
      data-slot={slot}
      data-mode={adding ? 'add' : 'list'}
      className="flex min-h-0 flex-1 flex-col bg-card"
    >
      <PanelHeader closeLabel={ts('Close')} onClose={onClose}>
        <div className="flex min-w-0 flex-1 items-center gap-2">
          <Icon
            className="size-5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
          <div className="min-w-0 flex-1 leading-tight">
            <h2 className="truncate text-base font-semibold text-foreground">
              {skills
                ? ts('Skills')
                : slot === 'dags'
                  ? ts('DAGs')
                  : ts('Tools')}
            </h2>
            <button
              type="button"
              data-testid="ve-slot-back"
              title={ts('Show the step {name}', { name: stepLabel })}
              className="flex min-w-0 items-center gap-0.5 rounded text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              onClick={onBack}
            >
              <ArrowLeft className="size-3 shrink-0" aria-hidden="true" />
              <span className="truncate">
                {ts('Back to step {name}', { name: stepLabel })}
              </span>
            </button>
          </div>
        </div>
        {total > 0 && (
          <span
            className="rounded bg-destructive/10 px-1.5 py-0.5 text-[10px] font-medium text-destructive"
            aria-label={ts('{count} issues', { count: total })}
          >
            {total}
          </span>
        )}
      </PanelHeader>
      {(!model.ok || step.broken !== undefined) && (
        <NoticeLine
          className="p-1"
          notices={[
            {
              id: 'slot-step-broken',
              tone: 'warning',
              priority: 0,
              message: (
                <>
                  <I18nText text="Fix the YAML errors to edit this step." />
                  {step.broken !== undefined && ` ${step.broken}`}
                </>
              ),
            },
          ]}
        />
      )}
      {!skills ? (
        <ToolsDock
          model={model}
          step={step}
          stepLabel={stepLabel}
          stepKind={agent ? 'agent' : 'chat'}
          scope={slot === 'dags' ? 'dags' : 'tools'}
          fieldIssues={fieldIssues}
          locked={locked}
          dag={dag}
          adding={adding}
          onEdit={onEdit}
          onOpenTool={onOpenTool}
          {...(onOpenDag ? { onOpenDag } : {})}
          onPick={onPickTool}
          onClose={onClose}
        />
      ) : (
        <SkillsDock
          step={step}
          stepLabel={stepLabel}
          fieldIssues={fieldIssues}
          locked={locked}
          adding={adding}
          onEdit={onEdit}
          onClose={onClose}
        />
      )}
    </div>
  );
}
