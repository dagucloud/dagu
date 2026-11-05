// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CircleDot, GitBranch, Maximize2, RotateCcw, X } from 'lucide-react';
import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import type { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/ui/icon-button';
import { PanelMessage } from '@/components/ui/panel-message';
import { StatusDot } from '@/components/ui/status-dot';
import {
  PANEL_HEADER_CLASS,
  PANEL_ZONE_SHELL_CLASS,
  panelMaxWidth,
} from '@/features/dags/components/step-details/PanelShell';
import { StepDetails } from '@/features/dags/components/step-details/StepDetails';
import { getStoredDrawerWidth } from '@/features/dags/components/step-details/StepSideDrawer';
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
 * The time of a double click. A panel that opens over the point of its click
 * lets the pointer through for this long, so that the second click still
 * reaches the step and opens its node view.
 */
const DOUBLE_CLICK_MS = 500;

type ClickPoint = Pick<MouseEvent, 'clientX' | 'clientY'>;

export interface StepPanelProps {
  /** Server name of the step. */
  step: string;
  /** The client point of the click that opened the panel; none for a link. */
  openedAt?: ClickPoint | null;
  onClose(): void;
  /** The step commands of the canvas host: the path of the node menu. */
  onStepCommand(cmd: StepCommand): void;
  gates: StepCommandGates;
  /** Opens the node view of the step, as a double click on it does. */
  onOpenStep(): void;
}

/**
 * The panel of one step over the right of the canvas. It does not take the
 * focus. Once a double click on the step that opened it is over, the canvas
 * fits the graph in the room that it leaves. It shows the step in the
 * selected run:
 * the facts line, Retry step and Change status (with the confirmations of
 * the node menu), Open step, then a Details tab (the run facts and the
 * definition) before the tabs of the Run panel for the step.
 */
export function StepPanel({
  step,
  openedAt = null,
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

  const ref = useRef<HTMLElement>(null);
  const [passThrough, setPassThrough] = useState(openedAt !== null);
  // The mark that makes the canvas fit beside the panel. A fit at once
  // would move the step from under the second click of a double click; a
  // click on another step during the wait starts the wait again.
  const [besideCanvas, setBesideCanvas] = useState(openedAt === null);
  useEffect(() => {
    if (besideCanvas) return;
    const timer = window.setTimeout(
      () => setBesideCanvas(true),
      DOUBLE_CLICK_MS
    );
    return () => window.clearTimeout(timer);
  }, [besideCanvas, openedAt]);
  useLayoutEffect(() => {
    const box = ref.current?.getBoundingClientRect();
    if (!openedAt || !box || !contains(box, openedAt)) {
      setPassThrough(false);
      return;
    }
    const timer = window.setTimeout(
      () => setPassThrough(false),
      DOUBLE_CLICK_MS
    );
    return () => window.clearTimeout(timer);
  }, [openedAt]);

  return (
    <aside
      ref={ref}
      aria-label={ts('Details of {step}', { step })}
      data-testid="ww-step-panel"
      data-step-panel=""
      data-panel-shell={besideCanvas ? 'right' : undefined}
      data-step={step}
      // The shell, the width and the header of every right panel: a trigger
      // panel and a step panel take turns as one pane.
      className={cn(
        PANEL_ZONE_SHELL_CLASS,
        'max-md:w-full',
        Z.ndv,
        passThrough && 'pointer-events-none'
      )}
      style={{ width: getStoredDrawerWidth(), maxWidth: panelMaxWidth() }}
    >
      <header className={PANEL_HEADER_CLASS}>
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

function contains(box: DOMRect, at: ClickPoint): boolean {
  return (
    at.clientX >= box.left &&
    at.clientX <= box.right &&
    at.clientY >= box.top &&
    at.clientY <= box.bottom
  );
}

/**
 * The run actions of the step. Like the node menu, a handler has no Retry
 * step and no Change status, a step with sub-runs opens them, and each
 * command meets its gate again in the host.
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
      {gates.hasSubRuns(step) && (
        <Button
          type="button"
          variant="outline"
          size="xs"
          onClick={(event) =>
            onStepCommand({
              kind: 'openSubRun',
              step,
              newTab: event.metaKey || event.ctrlKey,
            })
          }
        >
          <GitBranch aria-hidden="true" />
          {ts('Open sub-run')}
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
