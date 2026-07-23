// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';
import { render, waitFor } from '@testing-library/react';
import mermaid from 'mermaid';
import { describe, expect, it, vi } from 'vitest';

import {
  ControllerGraph,
  controllerGraphDefinition,
} from '../components/ControllerGraph';
import { createControllerDraft } from '../draft';

function supportMermaidLayout() {
  Object.defineProperty(SVGElement.prototype, 'getBBox', {
    configurable: true,
    value: () => ({ height: 20, width: 100, x: 0, y: 0 }),
  });
}

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

    const graph = controllerGraphDefinition(definition, 'done', 'LR');
    const diagram = await mermaid.mermaidAPI.getDiagramFromText(graph.mermaid);
    const database = diagram.db as {
      getVertices: () => Map<string, unknown>;
      getEdges: () => unknown[];
    };
    supportMermaidLayout();
    const rendered = await mermaid.render(
      'controller-graph-test',
      graph.mermaid
    );
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
    expect(
      container.querySelector('g.node.terminalFailed.current')
    ).not.toBeNull();
    expect(graph.mermaid).toContain('terminalFailed');
    expect(graph.mermaid).toContain('current');
    const edge = container.querySelector<SVGPathElement>(
      'g.edgePaths > path[data-id="controller_edge_0"]'
    );
    expect(edge).not.toBeNull();
    expect(graph.edgeConditions.get(edge?.dataset.id ?? '')).toBe(
      definition.states.default.transitions[0]?.when
    );
  });

  it('attaches each full condition to its rendered edge', async () => {
    const definition = createControllerDraft();
    const conditions = ['first full condition', 'second full condition'];
    definition.states.default!.transitions = conditions.map((when) => ({
      to: 'done',
      when,
    }));
    definition.states.done = {
      description: '',
      dags: [],
      transitions: [],
      terminal: 'succeeded',
    };
    supportMermaidLayout();

    const nativeBtoa = globalThis.btoa.bind(globalThis);
    const btoa = vi.spyOn(globalThis, 'btoa').mockImplementation((value) => {
      let binary = '';
      for (const byte of new TextEncoder().encode(value)) {
        binary += String.fromCharCode(byte);
      }
      return nativeBtoa(binary);
    });
    try {
      const view = render(
        React.createElement(ControllerGraph, {
          definition,
          currentState: 'default',
        })
      );

      await waitFor(() => {
        for (const [index, condition] of conditions.entries()) {
          expect(
            view.container.querySelector(
              `g.edgePaths > path[data-id="controller_edge_${index}"] > title`
            )
          ).toHaveTextContent(condition);
        }
      });
    } finally {
      btoa.mockRestore();
    }
  });
});
