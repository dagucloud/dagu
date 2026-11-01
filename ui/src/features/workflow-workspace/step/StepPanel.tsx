// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CircleDot, Maximize2, RotateCcw, X } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import type { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/ui/icon-button';
import { PanelMessage } from '@/components/ui/panel-message';
import { StatusDot } from '@/components/ui/status-dot';
import { StepDetails } from '@/features/dags/components/step-details/StepDetails';
import type {
  StepCommand,
  StepCommandGates,
} from '@/features/visual-editor/contracts';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import type { ApiNode, WorkspaceRunValue } from '../contracts';
import { useWorkspaceData } from '../data/context';
import { Z } from '../zLayers';
import { StepDeclared } from './StepDeclared';
import { stepDefinitionOf } from './stepDefinition';
import { StepFacts } from './StepFacts';
import { StepRunTabs } from './StepRunTabs';

type ApiStep = components['schemas']['Step'];

/**
 * The time of a double click. The panel lets the pointer through for this
 * long after it opens, so that a double click on a step that it opens over
 * still reaches the step and opens its node view.
 */
const DOUBLE_CLICK_MS = 500;

export interface StepPanelProps {
  /** Server name of the step. */
  step: string;
  onClose(): void;
  /** The step commands of the canvas host: the path of the node menu. */
  onStepCommand(cmd: StepCommand): void;
  gates: StepCommandGates;
  /** Opens the node view of the step, as a double click on it does. */
  onOpenStep(): void;
}

/**
 * The panel of one step over the right of the canvas. It does not move the
 * graph and does not take the focus. It shows the step in the selected run:
 * the facts line, Retry step and Change status (with the confirmations of
 * the node menu), Open step, then a Details tab (the run facts and the
 * definition) before the tabs of the Run panel for the step.
 */
export function StepPanel({
  step,
  onClose,
  onStepCommand,
  gates,
  onOpenStep,
}: StepPanelProps) {
  const { ts } = useI18n();
  const { dagData, fileName, run } = useWorkspaceData('StepPanel');
  const { handle } = useWorkspaceEditor();
  const node = run.nodeByStep.get(step);
  // The draft that the canvas shows, else the saved file; the run has the
  // definition that it ran.
  const definition =
    node?.step ?? stepDefinitionOf(handle?.getDag() ?? dagData.dag, step);

  const [armed, setArmed] = useState(false);
  useEffect(() => {
    const timer = window.setTimeout(() => setArmed(true), DOUBLE_CLICK_MS);
    return () => window.clearTimeout(timer);
  }, []);

  return (
    <aside
      aria-label={ts('Details of {step}', { step })}
      data-testid="ww-step-panel"
      data-step-panel=""
      data-step={step}
      className={cn(
        'absolute bottom-0 right-0 top-(--ww-toolbar-row,0px) flex w-[clamp(380px,40%,640px)] max-w-full flex-col border-l border-t border-border bg-card shadow-lg shadow-foreground/10 max-md:w-full',
        Z.ndv,
        !armed && 'pointer-events-none'
      )}
    >
      <header className="flex h-10 shrink-0 items-center gap-2 border-b border-border px-3">
        {node && (
          <StatusDot
            kind="step"
            status={node.status}
            decorative
            className="h-2 w-2 shrink-0"
          />
        )}
        <div className="min-w-0 flex-1 leading-tight">
          <p className="truncate text-[11px] text-muted-foreground">
            {dagData.dag?.name ?? fileName}
          </p>
          <h2 className="truncate text-sm font-semibold text-foreground">
            {step}
          </h2>
        </div>
        <IconButton icon={X} size="sm" label={ts('Close')} onClick={onClose} />
      </header>
      <div className="min-h-0 flex-1">
        <StepRunTabs
          // The tab chosen for one step is not the tab of the next one.
          key={step}
          step={step}
          runId={run.ref?.runId ?? null}
          declared={
            <div className="p-3">
              <StepDeclared step={step} node={node} definition={definition} />
            </div>
          }
          details={
            <StepPanelDetails
              step={step}
              run={run}
              node={node}
              definition={definition}
            />
          }
          toolbar={
            <StepPanelActions
              step={step}
              run={run}
              gates={gates}
              onStepCommand={onStepCommand}
              onOpenStep={onOpenStep}
            />
          }
        />
      </div>
    </aside>
  );
}

/**
 * The run actions of the step. Like the node menu, a handler has no Retry
 * step and no Change status, and each command meets its gate again in the
 * host.
 */
function StepPanelActions({
  step,
  run,
  gates,
  onStepCommand,
  onOpenStep,
}: {
  step: string;
  run: WorkspaceRunValue;
  gates: StepCommandGates;
  onStepCommand(cmd: StepCommand): void;
  onOpenStep(): void;
}) {
  const { ts } = useI18n();
  const handler = run.handlers.some((h) => h.node.step.name === step);
  const retry = !handler && gates.canRetry(step);
  const setStatus = !handler && gates.canSetStatus(step);
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-1.5 border-b border-border px-2 py-1.5">
      {retry && (
        <Button
          type="button"
          variant="outline"
          size="xs"
          onClick={() => onStepCommand({ kind: 'retryStep', step })}
        >
          <RotateCcw aria-hidden="true" />
          {ts('Retry step')}
        </Button>
      )}
      {setStatus && (
        <Button
          type="button"
          variant="outline"
          size="xs"
          onClick={() => onStepCommand({ kind: 'setStatus', step })}
        >
          <CircleDot aria-hidden="true" />
          {ts('Change status')}
        </Button>
      )}
      <Button type="button" variant="outline" size="xs" onClick={onOpenStep}>
        <Maximize2 aria-hidden="true" />
        {ts('Open step')}
      </Button>
    </div>
  );
}

/** The Details tab: the run facts of the step, then its definition. */
function StepPanelDetails({
  step,
  run,
  node,
  definition,
}: {
  step: string;
  run: WorkspaceRunValue;
  node: ApiNode | undefined;
  definition: ApiStep | undefined;
}) {
  const { ts } = useI18n();
  const holder = run.details;
  let facts: ReactNode = null;
  if (run.ref === null) {
    facts = (
      <PanelMessage tone="muted" align="start" className="p-0">
        {ts('Not run yet')}
      </PanelMessage>
    );
  } else if (holder && node) {
    facts = (
      <StepFacts
        target={{ step }}
        holder={holder}
        node={node}
        variant="details"
      />
    );
  } else if (holder) {
    facts = (
      <PanelMessage tone="muted" align="start" className="p-0">
        {ts('Not run in this run')}
      </PanelMessage>
    );
  } else {
    facts = (
      <PanelMessage tone="muted" align="start" className="p-0">
        {ts('Loading run...')}
      </PanelMessage>
    );
  }
  return (
    <div data-testid="ww-step-panel-details" className="space-y-3 p-3">
      {facts}
      {definition && (
        <details open className="space-y-2">
          <summary className="cursor-pointer text-xs font-medium text-muted-foreground">
            {ts('Definition')}
          </summary>
          <StepDetails step={definition} />
        </details>
      )}
    </div>
  );
}
