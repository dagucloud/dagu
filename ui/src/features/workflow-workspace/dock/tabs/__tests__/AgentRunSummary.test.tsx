// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { ApiDAGRunDetails } from '../../../contracts';
import { AgentRunSummary } from '../AgentRunSummary';

function run(extra: Partial<ApiDAGRunDetails>): ApiDAGRunDetails {
  return { name: 'agent', dagRunId: 'r1', ...extra } as ApiDAGRunDetails;
}

describe('AgentRunSummary', () => {
  it('shows the turns, the tool calls and the settled tasks', () => {
    render(
      <AgentRunSummary
        details={run({
          agentEvents: [
            { turn: 1, kind: 'action', name: 'plan', status: 'succeeded' },
            { turn: 2, kind: 'action', name: 'build', status: 'succeeded' },
            { turn: 2, kind: 'task_status', name: 'a', status: 'completed' },
          ] as never,
          agentTasks: [
            { name: 'a', status: 'completed' },
            { name: 'b', status: 'open' },
          ] as never,
        })}
      />
    );
    expect(screen.getByTestId('agent-run-iterations')).toHaveTextContent(
      'Iterations: 2'
    );
    expect(screen.getByTestId('agent-run-calls')).toHaveTextContent(
      'Tool calls: 2'
    );
    expect(screen.getByTestId('agent-run-tasks')).toHaveTextContent(
      '1 of 2 tasks settled'
    );
  });

  it('leaves out the tasks line when the run has no task', () => {
    render(<AgentRunSummary details={run({ agentTasks: [] as never })} />);
    expect(screen.getByTestId('agent-run-iterations')).toHaveTextContent(
      'Iterations: 0'
    );
    expect(screen.queryByTestId('agent-run-tasks')).toBeNull();
  });

  it('shows nothing for a run that is not an agent run', () => {
    const { container } = render(<AgentRunSummary details={run({})} />);
    expect(container).toBeEmptyDOMElement();
  });
});
