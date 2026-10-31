// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { SlidersHorizontal } from 'lucide-react';
import { useMemo, useState, type ReactElement, type ReactNode } from 'react';
import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { buildDetailsGraph, VisualCanvas } from './canvas';
import { useCanvasDirection } from './canvas/useCanvasDirection';
import type {
  ApiDAGDetails,
  CanvasGraph,
  RunOverlay,
  StepKey,
} from './contracts';
import { WorkflowDrawer } from './WorkflowDrawer';

export interface DetailsCanvasProps {
  dag: ApiDAGDetails;
  /** Controls at the top right of the canvas. */
  toolbarEnd?: ReactNode;
  /** Controls before the others, as the editor puts the document selector. */
  toolbarLead?: ReactNode;
  /** Controls after the Workflow button, as the editor puts its actions. */
  toolbarTrail?: ReactNode;
  /** What the Workflow panel shows; the panel has no button without it. */
  workflowPanel?: ReactNode;
  /** A step was picked: the caller shows its details. Null clears it. */
  onSelectStep(stepName: string | null): void;
  /** A step was opened with Enter or a double click, after its selection. */
  onOpenStep?(stepName: string): void;
  /** Run state of each step, by step name. The nodes show it. */
  overlay?: RunOverlay;
  /** Controlled selection, by step name. Given, the canvas keeps no selection of its own. */
  selectedStepName?: string;
  /** Classes for the frame of the canvas. */
  className?: string;
  /** Margin around the graph when it fits; the canvas default otherwise. */
  fitPadding?: number;
}

function withOverlay(graph: CanvasGraph, overlay?: RunOverlay): CanvasGraph {
  if (!overlay) return graph;
  return {
    ...graph,
    nodes: graph.nodes.map((node) =>
      Object.prototype.hasOwnProperty.call(overlay, node.label)
        ? { ...node, run: overlay[node.label] }
        : node
    ),
  };
}

/**
 * Read-only canvas of a DAG that the API describes, such as a sub-DAG of a
 * file with several documents. It shows the same shapes as the editor.
 */
export function DetailsCanvas({
  dag,
  toolbarEnd,
  toolbarLead,
  toolbarTrail,
  workflowPanel,
  onSelectStep,
  onOpenStep,
  overlay,
  selectedStepName,
  className,
  fitPadding,
}: DetailsCanvasProps): ReactElement {
  const { ts } = useI18n();
  const graph = useMemo(
    () => withOverlay(buildDetailsGraph(dag), overlay),
    [dag, overlay]
  );
  const [ownSelected, setSelected] = useState<StepKey | null>(null);
  const selected =
    selectedStepName === undefined
      ? ownSelected
      : (graph.nodes.find((node) => node.label === selectedStepName)?.key ??
        null);
  const [direction, setDirection] = useCanvasDirection();
  const [workflowOpen, setWorkflowOpen] = useState(false);

  const select = (key: StepKey | null) => {
    setSelected(key);
    if (key !== null) setWorkflowOpen(false);
    const node = graph.nodes.find((item) => item.key === key);
    onSelectStep(node ? node.label : null);
  };

  const end = (
    <>
      {toolbarLead}
      {toolbarEnd}
      {workflowPanel && (
        <Button
          type="button"
          variant="ghost"
          className={cn('h-7 px-2 text-xs', workflowOpen && 'bg-muted')}
          title={ts('Workflow')}
          aria-label={ts('Workflow')}
          aria-pressed={workflowOpen}
          onClick={() => {
            if (!workflowOpen) {
              setSelected(null);
              onSelectStep(null);
            }
            setWorkflowOpen(!workflowOpen);
          }}
        >
          <SlidersHorizontal className="h-3.5 w-3.5" />
        </Button>
      )}
      {toolbarTrail}
    </>
  );

  return (
    <div
      className={cn(
        'relative min-h-[420px] min-w-0 flex-1 overflow-hidden rounded-md border',
        className
      )}
    >
      <VisualCanvas
        graph={graph}
        direction={direction}
        selected={selected}
        frozen={false}
        readOnly
        catalog={[]}
        onSelect={select}
        onEdit={() => {}}
        onCommand={(command) => {
          if (command.kind !== 'openInspector') return;
          select(command.step);
          const node = graph.nodes.find((item) => item.key === command.step);
          if (node) onOpenStep?.(node.label);
        }}
        onDirectionChange={setDirection}
        fitPadding={fitPadding}
        toolbarEnd={end}
        exportName={dag.name}
      />
      {workflowPanel && (
        <WorkflowDrawer
          open={workflowOpen}
          onClose={() => setWorkflowOpen(false)}
        >
          {workflowPanel}
        </WorkflowDrawer>
      )}
    </div>
  );
}

export default DetailsCanvas;
