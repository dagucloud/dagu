// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { readAgentHeader } from '../agentHeader';

describe('readAgentHeader', () => {
  it('reads the model, the first line of the prompt, the limit and the tasks', () => {
    const header = readAgentHeader(`type: agent
llm:
  provider: anthropic
  model: claude-opus-5
  max_tool_iterations: 30
  system: |

    You ship a small release.
    Never publish without approval.
tasks:
  - name: planned
    description: Finished when the plan exists.
  - name: published
`);
    expect(header).toEqual({
      provider: 'anthropic',
      model: 'claude-opus-5',
      maxIterations: 30,
      systemFirstLine: 'You ship a small release.',
      tasks: [
        { name: 'planned', description: 'Finished when the plan exists.' },
        { name: 'published' },
      ],
    });
  });

  it('gives no model and no tasks for text that is not an agent', () => {
    expect(readAgentHeader('steps:\n  - run: echo\n')).toEqual({ tasks: [] });
    expect(readAgentHeader('')).toEqual({ tasks: [] });
    expect(readAgentHeader('- a\n- b\n')).toEqual({ tasks: [] });
  });

  // The server reads a quoted string whatever the indent of its later lines.
  it('reads a task description with unindented lines', () => {
    const header = readAgentHeader(
      "type: agent\ntasks:\n  - name: report\n    description: 'Write the\nreport'\n"
    );
    expect(header.tasks).toEqual([
      { name: 'report', description: 'Write the report' },
    ]);
  });

  it('never throws on broken YAML', () => {
    expect(readAgentHeader('llm: [unclosed\n  tasks: {')).toEqual({
      tasks: [],
    });
  });

  it('skips a task with no name and a limit that is not a number', () => {
    const header = readAgentHeader(
      'llm:\n  max_tool_iterations: many\ntasks:\n  - description: x\n  - name: ok\n  - 3\n'
    );
    expect(header.maxIterations).toBeUndefined();
    expect(header.tasks).toEqual([{ name: 'ok' }]);
  });
});
