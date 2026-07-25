// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';
import { ArrowDownUp, ArrowRightLeft, ZoomIn, ZoomOut } from 'lucide-react';

import Mermaid from '@/components/ui/mermaid';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { ControllerDefinition } from '../types';

type Direction = 'TD' | 'LR';

function mermaidText(value: string): string {
  return Array.from(value, (character) => `#${character.codePointAt(0)};`).join(
    ''
  );
}

function shortCondition(value: string): string {
  const firstLine =
    value.split(/\r\n|[\n\v\f\r\u0085\u2028\u2029]/u, 1)[0] ?? '';
  const characters = Array.from(firstLine);
  return characters.length > 64
    ? `${characters.slice(0, 61).join('')}…`
    : firstLine;
}

export function controllerGraphDefinition(
  definition: ControllerDefinition,
  currentState: string | undefined,
  direction: Direction
): { mermaid: string; edgeConditions: ReadonlyMap<string, string> } {
  const stateEntries = Object.entries(definition.states);
  const ids = new Map(
    stateEntries.map(([name], index) => [name, `controller_state_${index}`])
  );
  const lines = [`flowchart ${direction}`];
  const edgeConditions = new Map<string, string>();

  for (const [name, state] of stateEntries) {
    const id = ids.get(name)!;
    const labelParts = [name];
    const dagCount = `${state.dags.length} ${
      state.dags.length === 1 ? 'DAG' : 'DAGs'
    }`;
    const transitionCount = `${state.transitions.length} outgoing`;
    if (name === 'default') {
      labelParts.push(`INITIAL · ${dagCount} · ${transitionCount}`);
    } else if (state.terminal) {
      labelParts.push(
        `${state.terminal.toUpperCase()} · ${dagCount} · ${transitionCount}`
      );
    } else {
      labelParts.push(`${dagCount} · ${transitionCount}`);
    }
    const label = labelParts.map(mermaidText).join('\\n');
    const shape = state.terminal ? `[["${label}"]]` : `["${label}"]`;
    const classes = [
      state.terminal === 'succeeded' ? 'terminalSuccess' : '',
      state.terminal === 'failed' ? 'terminalFailed' : '',
      name === currentState ? 'current' : '',
    ].filter(Boolean);
    lines.push(`${id}${shape};`);
    for (const className of classes) {
      lines.push(`class ${id} ${className};`);
    }
  }

  let edgeIndex = 0;
  for (const [name, state] of stateEntries) {
    const source = ids.get(name)!;
    for (const transition of state.transitions) {
      const target = ids.get(transition.to);
      if (!target) continue;
      const edgeID = `controller_edge_${edgeIndex}`;
      lines.push(
        `${source} ${edgeID}@-->|"${mermaidText(shortCondition(transition.when))}"| ${target};`
      );
      edgeConditions.set(edgeID, transition.when);
      edgeIndex += 1;
    }
  }

  lines.push(
    'classDef current fill:#eef2ff,stroke:#7c3aed,stroke-width:4px,color:#111827;'
  );
  lines.push('classDef terminalSuccess stroke:#22c55e,stroke-width:3px;');
  lines.push('classDef terminalFailed stroke:#ef4444,stroke-width:3px;');
  return { mermaid: lines.join('\n'), edgeConditions };
}

function addEdgeTitles(
  container: HTMLDivElement,
  conditions: ReadonlyMap<string, string>
): void {
  container
    .querySelectorAll<SVGPathElement>('g.edgePaths > path[data-id]')
    .forEach((edge) => {
      const condition = conditions.get(edge.dataset.id ?? '');
      if (!condition) return;
      edge.querySelector('title')?.remove();
      const title = document.createElementNS(
        'http://www.w3.org/2000/svg',
        'title'
      );
      title.textContent = condition;
      edge.prepend(title);
    });
  container.querySelectorAll<SVGGElement>('g.node.current').forEach((node) => {
    node.classList.add('animate-pulse');
  });
}

export function ControllerGraph({
  definition,
  currentState,
  className,
}: {
  definition: ControllerDefinition;
  currentState?: string;
  className?: string;
}) {
  const [direction, setDirection] = React.useState<Direction>('TD');
  const [scale, setScale] = React.useState(1);
  const graph = React.useMemo(
    () => controllerGraphDefinition(definition, currentState, direction),
    [currentState, definition, direction]
  );
  const onRender = React.useCallback(
    (container: HTMLDivElement) =>
      addEdgeTitles(container, graph.edgeConditions),
    [graph.edgeConditions]
  );

  return (
    <div
      className={cn(
        'relative min-h-[360px] overflow-hidden rounded-md border border-border bg-card',
        className
      )}
    >
      <div className="absolute right-2 top-2 z-10 flex gap-1 rounded-md border border-border bg-card p-1 shadow-sm">
        <Button
          variant={direction === 'TD' ? 'primary' : 'ghost'}
          size="icon-sm"
          aria-label="Vertical graph layout"
          onClick={() => setDirection('TD')}
        >
          <ArrowDownUp className="h-4 w-4" />
        </Button>
        <Button
          variant={direction === 'LR' ? 'primary' : 'ghost'}
          size="icon-sm"
          aria-label="Horizontal graph layout"
          onClick={() => setDirection('LR')}
        >
          <ArrowRightLeft className="h-4 w-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Zoom in"
          onClick={() => setScale((value) => Math.min(2, value + 0.1))}
        >
          <ZoomIn className="h-4 w-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Zoom out"
          onClick={() => setScale((value) => Math.max(0.4, value - 0.1))}
        >
          <ZoomOut className="h-4 w-4" />
        </Button>
      </div>
      <Mermaid
        def={graph.mermaid}
        scale={scale}
        onRender={onRender}
        style={{ minHeight: 360, paddingTop: 32 }}
        fallback={
          <ol className="space-y-2 p-6 text-sm">
            {Object.entries(definition.states).map(([name, state]) => (
              <li key={name}>
                <strong>{name}</strong>
                {state.terminal ? ` — terminal ${state.terminal}` : ''}
              </li>
            ))}
          </ol>
        }
      />
    </div>
  );
}
