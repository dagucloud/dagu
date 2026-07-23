// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import mermaid from 'mermaid';
import { describe, expect, it } from 'vitest';

import { controllerGraphDefinition } from '../components/ControllerGraph';
import { createControllerDraft } from '../draft';

describe('Controller graph adapter', () => {
  it('parses user text as labels and bounds the visible condition', async () => {
    const definition = createControllerDraft();
    const firstLine = `%%{init: {"flowchart": {"htmlLabels": false}}}%% | "<img>${'😀'.repeat(70)}`;
    definition.states.default = {
      description: '',
      dags: ['notify | escaped'],
      transitions: [
        {
          to: 'done',
          when: `${firstLine}\u2028This line is not shown`,
        },
      ],
    };
    definition.states.done = {
      description: '',
      dags: [],
      transitions: [],
      terminal: 'failed',
    };

    const graph = controllerGraphDefinition(definition, 'default', 'LR');
    const diagram = await mermaid.mermaidAPI.getDiagramFromText(graph.mermaid);
    const database = diagram.db as {
      getVertices: () => Map<string, unknown>;
      getEdges: () => unknown[];
    };
    Object.defineProperty(SVGElement.prototype, 'getBBox', {
      configurable: true,
      value: () => ({ height: 20, width: 100, x: 0, y: 0 }),
    });
    const rendered = await mermaid.render('controller-graph-test', graph.mermaid);
    const container = document.createElement('div');
    container.innerHTML = rendered.svg;

    expect(graph.mermaid).toContain('flowchart LR');
    expect([...database.getVertices().keys()]).toEqual([
      'controller_state_0',
      'controller_state_1',
    ]);
    expect(database.getEdges()).toHaveLength(1);
    expect(container.querySelector('.edgeLabel')).toHaveTextContent(
      `${Array.from(firstLine).slice(0, 61).join('')}…`
    );
    expect(graph.mermaid).toContain('terminalFailed');
    expect(graph.mermaid).toContain('current');
    expect(graph.edgeConditions).toEqual([
      definition.states.default.transitions[0]?.when,
    ]);
  });
});
