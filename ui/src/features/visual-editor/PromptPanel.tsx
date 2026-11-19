// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ArrowLeft, MessageSquareText } from 'lucide-react';
import { useEffect, useMemo, useRef, type ReactElement } from 'react';
import { NoticeLine } from '@/features/dags/components/dag-editor/NoticeLine';
import { PanelHeader } from '@/features/dags/components/step-details/PanelShell';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import type {
  DagDocModel,
  DagEditOp,
  ReferenceCandidate,
  StepView,
  ValidationIssue,
} from './contracts';
import { focusBox } from './inspector/focusField';
import { issuesUnder, stepIssues } from './inspector/issues';
import { Section } from './inspector/Section';
import {
  PromptSection,
  SystemSection,
} from './inspector/sections/chat/PromptSections';

/** Lines of the boxes of the dock: it is tall, so the texts get room. */
const SYSTEM_ROWS = 10;
const PROMPT_ROWS = 24;

export interface PromptPanelProps {
  model: DagDocModel;
  /** The agent step that holds the texts. */
  step: StepView;
  /** The name of the step, for the way back to it. */
  stepLabel: string;
  /** The issues of the step. */
  issues: ValidationIssue[];
  candidates: ReferenceCandidate[];
  readOnly: boolean;
  onEdit(op: DagEditOp): void;
  /** Shows the step itself: its form. */
  onBack(): void;
  onClose(): void;
}

/**
 * The right-hand dock of the Prompt card of an agent step. It edits both
 * texts of the step, `with.system` and `with.prompt`, in the boxes of the
 * form (with the insert of a reference), under the title bar that the panel
 * of a tool has. Every change is an operation on the YAML, which stays the
 * one source. The dock is read-only when `readOnly` is set or the YAML has
 * syntax errors. It takes the focus on the prompt when it opens, unless it is
 * read-only.
 */
export function PromptPanel({
  model,
  step,
  stepLabel,
  issues,
  candidates,
  readOnly,
  onEdit,
  onBack,
  onClose,
}: PromptPanelProps): ReactElement {
  const { ts } = useI18n();
  const grouped = useMemo(
    () => stepIssues(issues, step, ts),
    [issues, step, ts]
  );
  const fieldIssues = grouped.byField;
  const locked = readOnly || !model.ok || step.broken !== undefined;
  const bodyRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (locked) return;
    const box = bodyRef.current?.querySelector<HTMLElement>(
      '[data-field-path="with.prompt"]'
    );
    if (box) focusBox(box);
    // Once: the host keys the panel by the step.
  }, []);
  const total =
    issuesUnder(fieldIssues, 'with.system').length +
    issuesUnder(fieldIssues, 'with.prompt').length;
  const common = { step, readOnly: locked, onEdit, fieldIssues, candidates };
  return (
    <div
      data-testid="ve-prompt-panel"
      className="flex min-h-0 flex-1 flex-col bg-card"
    >
      <PanelHeader closeLabel={ts('Close')} onClose={onClose}>
        <div className="flex min-w-0 flex-1 items-center gap-2">
          <MessageSquareText
            className="size-5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
          <div className="min-w-0 flex-1 leading-tight">
            <h2 className="truncate text-base font-semibold text-foreground">
              {ts('Prompt')}
            </h2>
            <button
              type="button"
              data-testid="ve-prompt-back"
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
      <div ref={bodyRef} className="min-h-0 flex-1 overflow-auto">
        {(!model.ok || step.broken !== undefined) && (
          <NoticeLine
            className="p-1"
            notices={[
              {
                id: 'prompt-step-broken',
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
        <Section
          title={<I18nText text="System message" />}
          count={issuesUnder(fieldIssues, 'with.system').length}
        >
          <SystemSection {...common} rows={SYSTEM_ROWS} />
        </Section>
        <Section
          title={<I18nText text="Prompt" />}
          count={issuesUnder(fieldIssues, 'with.prompt').length}
        >
          <PromptSection {...common} messages={false} rows={PROMPT_ROWS} />
        </Section>
      </div>
    </div>
  );
}
