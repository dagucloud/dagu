// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';

import { controllerGraphDefinition } from '../components/ControllerGraph';
import { createControllerDraft } from '../draft';

describe('Controller graph adapter', () => {
  it('uses internal IDs, terminal styles, and a bounded edge label', () => {
    const definition = createControllerDraft();
    definition.states.default = {
      description: '',
      dags: [],
      transitions: [
        {
          to: 'done',
          when: `Condition with a quote " and <markup> ${'x'.repeat(80)}`,
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

    expect(graph.mermaid).toContain('flowchart LR');
    expect(graph.mermaid).toContain('controller_state_0');
    expect(graph.mermaid).toContain('terminalFailed');
    expect(graph.mermaid).toContain('current');
    expect(graph.mermaid).not.toContain('<markup>');
    expect(graph.edgeConditions).toEqual([
      definition.states.default.transitions[0]?.when,
    ]);
  });
});
