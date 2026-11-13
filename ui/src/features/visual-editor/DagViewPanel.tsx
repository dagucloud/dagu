// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronRight, Repeat, Workflow } from 'lucide-react';
import {
  useEffect,
  useMemo,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react';
import { Button } from '@/components/ui/button';
import { StatusDot } from '@/components/ui/status-dot';
import { PanelHeader } from '@/features/dags/components/step-details/PanelShell';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import { AgentRunContext } from './canvas/agentRunContext';
import { CanvasToolbarHostContext } from './canvas/toolbarHost';
import { CanvasOverlayInsetContext } from './canvas/overlayInset';
import { ToolRunContext, useDagToolRun } from './canvas/toolRun';
import type { ApiStep, StepView, ToolSelection } from './contracts';
import DetailsCanvas from './DetailsCanvas';
import { buildRunOverlay } from './run/runOverlay';
import { findToolEntry } from './ToolPanel';
import {
  backTo,
  currentDag,
  isRecursive,
  openDag,
  startPath,
  stepFacts,
  type DagPath,
} from './tools/dagPath';
import { mergeInputs } from './tools/dockModel';
import { InputChip } from './tools/InputChip';
import { OpenIcon, OpenTextLink } from './tools/OpenLink';
import { dagLink } from './tools/targets';
import { useDagDetails } from './tools/useDagCatalog';

export interface DagViewPanelProps {
  /** The step that holds the DAG tool. */
  step: StepView;
  /** The name of the step, for the way back to it. */
  stepLabel: string;
  /** The server name of the step: a run view finds its sub-run by it. */
  stepName?: string | undefined;
  /** The DAG tool whose DAG the panel shows. */
  tool: ToolSelection;
  /** The DAG that holds the step: a call back to it is a loop. */
  host: string;
  /** Shows the panel of the tool: its inputs. */
  onShowTool(): void;
  /** Shows the step itself: its form. */
  onBack(): void;
  onClose(): void;
}

// The inner canvas draws its own toolbar and knows no run of the step above.
function Isolated({ children }: { children: ReactNode }): ReactElement {
  return (
    <CanvasToolbarHostContext.Provider value={undefined}>
      <CanvasOverlayInsetContext.Provider value={{ right: 0 }}>
        <ToolRunContext.Provider value={null}>
          <AgentRunContext.Provider value={null}>
            {children}
          </AgentRunContext.Provider>
        </ToolRunContext.Provider>
      </CanvasOverlayInsetContext.Provider>
    </CanvasToolbarHostContext.Provider>
  );
}

function Facts({
  step,
  path,
  onOpen,
}: {
  step: ApiStep | undefined;
  path: DagPath;
  onOpen(name: string): void;
}): ReactElement {
  const { ts } = useI18n();
  if (!step) {
    return (
      <p
        data-testid="ve-dag-step-empty"
        className="text-xs text-muted-foreground"
      >
        {ts('Select a step to see its details.')}
      </p>
    );
  }
  const facts = stepFacts(step);
  const loop = facts.call !== undefined && isRecursive(path, facts.call);
  return (
    <div
      data-testid="ve-dag-step"
      data-step={facts.name}
      className="space-y-1.5"
    >
      <div className="flex min-w-0 items-center gap-2">
        <h3 className="min-w-0 truncate text-sm font-semibold">{facts.name}</h3>
        <span
          className={cn(
            'shrink-0 rounded px-1.5 text-[10px] font-medium leading-4',
            facts.action === 'call'
              ? 'bg-tool-dag/10 text-tool-dag'
              : 'bg-muted text-muted-foreground'
          )}
        >
          {facts.action}
        </span>
        {facts.parallel && (
          <span className="shrink-0 text-[11px] text-muted-foreground">
            {ts('Parallel')}
          </span>
        )}
      </div>
      {facts.description && (
        <p className="whitespace-pre-line break-words text-xs text-foreground">
          {facts.description}
        </p>
      )}
      {facts.call !== undefined && (
        <div
          data-testid="ve-dag-step-call"
          className="flex flex-wrap items-center gap-2 text-xs"
        >
          <span className="text-muted-foreground">
            {ts('Calls {name}', { name: facts.call })}
          </span>
          {loop ? (
            <span
              data-testid="ve-dag-loop"
              role="status"
              className="inline-flex items-center gap-1 rounded bg-muted px-1.5 text-[11px] leading-5 text-muted-foreground"
            >
              <Repeat className="size-3" aria-hidden="true" />
              {ts('Open already: a call that loops is shown once.')}
            </span>
          ) : (
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-6 px-2 text-xs"
              data-testid="ve-dag-step-open"
              onClick={() => onOpen(facts.call as string)}
            >
              {ts('Open {name} here', { name: facts.call })}
            </Button>
          )}
        </div>
      )}
      {facts.command.length > 0 && (
        <pre className="max-h-20 overflow-auto rounded bg-muted px-2 py-1 font-mono text-[11px] leading-4 text-foreground">
          {facts.command.join('\n')}
        </pre>
      )}
      {facts.depends.length > 0 && (
        <p className="text-[11px] text-muted-foreground">
          {ts('Depends on')}: {facts.depends.join(', ')}
        </p>
      )}
      {facts.conditions.length > 0 && (
        <ul className="space-y-0.5">
          {facts.conditions.map((line) => (
            <li
              key={line}
              className="break-all font-mono text-[11px] text-muted-foreground"
            >
              {ts('Only if')} {line}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * The enlarged view of the DAG that a DAG tool calls, in the dock at the right.
 * The title bar has the DAG and a breadcrumb that starts at the calling step:
 * a click on the step returns to its form, a click on a DAG of the path
 * returns to it. The body has the description of the DAG, a link to its page,
 * its inputs, a read-only canvas of its steps (the canvas of the DAG page, with
 * zoom, pan, fit and the direction of the flow), and the facts of the selected
 * step. A step that calls another DAG has a button that opens that DAG in the
 * same view, one level deeper; a call that goes back to a DAG that is open
 * already, or to the DAG that holds the step, is shown once and never opened,
 * so that a loop ends. In a run view the canvas has the state of each step of
 * the latest sub-run, and the title bar has its status and a link to it. A DAG
 * that the server does not know shows the reason in place of the canvas.
 */
export function DagViewPanel({
  step,
  stepLabel,
  stepName,
  tool,
  host,
  onShowTool,
  onBack,
  onClose,
}: DagViewPanelProps): ReactElement {
  const { ts } = useI18n();
  const entry = useMemo(
    () => findToolEntry(step, tool),
    [step, tool.category, tool.index]
  );
  const rootName = entry?.kind === 'dag' ? entry.label.trim() : '';
  const [path, setPath] = useState<DagPath>(() => startPath(host, rootName));
  const [picked, setPicked] = useState<string | null>(null);
  const shown = currentDag(path);
  const depth = path.opened.length;
  // A DAG that the entry names by another name starts a new path.
  useEffect(() => {
    setPath(startPath(host, rootName));
    setPicked(null);
  }, [host, rootName]);
  const details = useDagDetails(shown, rootName !== '');
  const child = useDagToolRun(stepName, rootName, depth === 1, false);
  const overlay = useMemo(
    () =>
      depth === 1 && child?.details
        ? buildRunOverlay(child.details)
        : undefined,
    [depth, child?.details]
  );
  const inputs = useMemo(
    () =>
      mergeInputs(
        depth === 1 ? (entry?.params ?? []) : [],
        details.known === true ? details.inputs : null
      ),
    [depth, entry?.params, details.known, details.inputs]
  );
  const page = dagLink(shown);
  const steps = details.dag?.steps ?? [];
  const selected = picked
    ? steps.find((item) => item.name === picked)
    : undefined;
  const open = (name: string) => {
    setPath((current) => openDag(current, name));
    setPicked(null);
  };
  const goTo = (level: number) => {
    setPath((current) => backTo(current, level));
    setPicked(null);
  };

  if (!entry || entry.kind !== 'dag') {
    return (
      <div
        data-testid="ve-dag-panel"
        className="flex min-h-0 flex-1 flex-col bg-card"
      >
        <PanelHeader
          title={<I18nText text="This tool no longer exists." />}
          closeLabel={ts('Close')}
          onClose={onClose}
        />
      </div>
    );
  }
  const crumbClass =
    'min-w-0 max-w-[10rem] truncate rounded hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring';
  return (
    <div
      data-testid="ve-dag-panel"
      data-dag={shown}
      data-depth={depth}
      className="flex min-h-0 flex-1 flex-col bg-card"
    >
      <PanelHeader closeLabel={ts('Close')} onClose={onClose}>
        <div className="flex min-w-0 flex-1 items-center gap-2">
          <Workflow
            className="size-5 shrink-0 text-tool-dag"
            aria-hidden="true"
          />
          <div className="min-w-0 flex-1 leading-tight">
            <h2
              data-testid="ve-dag-title"
              className="truncate text-base font-semibold text-foreground"
            >
              {shown}
            </h2>
            <nav aria-label={ts('Path of the DAG')} data-testid="ve-dag-crumbs">
              <ol className="flex min-w-0 items-center gap-0.5 text-xs text-muted-foreground">
                <li className="flex min-w-0 items-center">
                  <button
                    type="button"
                    data-testid="ve-dag-crumb-step"
                    title={ts('Show the step {name}', { name: stepLabel })}
                    className={crumbClass}
                    onClick={onBack}
                  >
                    {stepLabel}
                  </button>
                </li>
                {path.opened.map((name, index) => {
                  const last = index === depth - 1;
                  return (
                    <li
                      key={`${index}:${name}`}
                      className="flex min-w-0 items-center gap-0.5"
                    >
                      <ChevronRight
                        className="size-3 shrink-0"
                        aria-hidden="true"
                      />
                      {last ? (
                        <span
                          aria-current="page"
                          className="min-w-0 max-w-[10rem] truncate font-medium text-foreground"
                        >
                          {name}
                        </span>
                      ) : (
                        <button
                          type="button"
                          data-testid="ve-dag-crumb"
                          className={crumbClass}
                          onClick={() => goTo(index + 1)}
                        >
                          {name}
                        </button>
                      )}
                    </li>
                  );
                })}
              </ol>
            </nav>
          </div>
        </div>
        {depth === 1 && child?.status !== undefined && (
          <StatusDot kind="run" status={child.status} />
        )}
        {depth === 1 && child?.link && (
          <OpenIcon
            link={child.link}
            name={ts('{name} run', { name: shown })}
          />
        )}
      </PanelHeader>
      <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-auto p-3">
        <section className="space-y-1.5">
          {details.description ? (
            <p
              data-testid="ve-dag-panel-description"
              className="line-clamp-3 whitespace-pre-line break-words text-xs text-foreground"
            >
              {details.description}
            </p>
          ) : details.known === true ? (
            <p className="text-xs italic text-muted-foreground">
              {ts('This DAG has no description.')}
            </p>
          ) : null}
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            {page && (
              <OpenTextLink page={page}>
                <I18nText text="Open the workflow" />
              </OpenTextLink>
            )}
            {depth === 1 && (
              <button
                type="button"
                data-testid="ve-dag-edit-inputs"
                className="rounded text-xs text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                onClick={onShowTool}
              >
                {ts('Edit the tool')}
              </button>
            )}
          </div>
          {inputs.length > 0 && (
            <div
              data-testid="ve-dag-panel-inputs"
              className="flex flex-wrap items-center gap-1"
            >
              <span className="text-[11px] text-muted-foreground">
                {ts('Inputs')}
              </span>
              {inputs.map((input) => (
                <InputChip key={input.name} input={input} />
              ))}
            </div>
          )}
        </section>
        {details.known === false ? (
          <p
            role="alert"
            data-testid="ve-dag-missing"
            className="rounded-md border border-dashed border-destructive/60 px-3 py-6 text-center text-xs text-destructive"
          >
            {ts(
              'The server does not know the DAG "{name}". The step fails when it starts.',
              { name: shown }
            )}
          </p>
        ) : details.dag && steps.length === 0 ? (
          <p className="rounded-md border border-dashed border-border px-3 py-6 text-center text-xs text-muted-foreground">
            {ts('This DAG has no steps.')}
          </p>
        ) : details.dag ? (
          <Isolated>
            <div
              role="group"
              aria-label={ts('Workflow graph')}
              data-testid="ve-dag-graph"
              className="flex min-h-[260px] flex-1 flex-col"
            >
              <DetailsCanvas
                // One canvas for each DAG: it fits its own graph when it opens.
                key={shown}
                dag={details.dag}
                {...(overlay ? { overlay } : {})}
                selectedStepName={picked ?? ''}
                onSelectStep={setPicked}
                className="min-h-[260px]"
              />
            </div>
          </Isolated>
        ) : (
          <p className="text-xs text-muted-foreground">{ts('Loading...')}</p>
        )}
        {details.dag && steps.length > 0 && (
          <section
            aria-label={ts('Step')}
            className="shrink-0 rounded-md border border-border p-2.5"
          >
            <Facts step={selected} path={path} onOpen={open} />
          </section>
        )}
      </div>
    </div>
  );
}
