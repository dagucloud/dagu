// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AgentSessionState, NodeStatus } from '@/api/v1/schema';
import { useClient } from '@/hooks/api';
import { AgentSessionTab } from '../AgentSessionTab';

vi.mock('@/hooks/api', () => ({
  useClient: vi.fn(),
}));

vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const useClientMock = vi.mocked(useClient);
const sessionError = 'Model not found';

function agentNode(
  name: string,
  state: AgentSessionState,
  status: NodeStatus,
  eventContent?: string
) {
  return {
    step: { name },
    status,
    agentSession: {
      provider: 'opencode',
      state,
      lastError: sessionError,
      events: eventContent
        ? [
            {
              sequence: 1,
              id: `${name}-event`,
              type: 'text',
              content: eventContent,
            },
          ]
        : [],
    },
  };
}

function dagRun(nodes: ReturnType<typeof agentNode>[]) {
  return {
    dagRunId: 'run-1',
    name: 'agent-workflow',
    rootDAGRunId: 'run-1',
    rootDAGRunName: 'agent-workflow',
    nodes,
  } as never;
}

function AgentSessionHarness({
  nodes,
}: {
  nodes: ReturnType<typeof agentNode>[];
}) {
  const [selectedStep, setSelectedStep] = React.useState('');
  return (
    <AgentSessionTab
      dagRun={dagRun(nodes)}
      onChanged={vi.fn()}
      selectedStep={selectedStep}
      onSelectedStepChange={setSelectedStep}
    />
  );
}

function renderAgentSessions(nodes: ReturnType<typeof agentNode>[]) {
  return render(<AgentSessionHarness nodes={nodes} />);
}

describe('AgentSessionTab', () => {
  beforeEach(() => {
    useClientMock.mockReturnValue({ POST: vi.fn() } as never);
  });

  it('shows the provider error when the session failed', () => {
    renderAgentSessions([
      agentNode('implement', AgentSessionState.failed, NodeStatus.Failed),
    ]);

    expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
    expect(screen.getByText(sessionError)).toBeInTheDocument();
  });

  it('does not show a stale provider error after the session succeeds', () => {
    renderAgentSessions([
      agentNode('implement', AgentSessionState.succeeded, NodeStatus.Success),
    ]);

    expect(screen.queryByText(sessionError)).not.toBeInTheDocument();
  });

  it('selects the session needing attention and renders one conversation', () => {
    renderAgentSessions([
      agentNode(
        'analyze',
        AgentSessionState.succeeded,
        NodeStatus.Success,
        'Analysis complete'
      ),
      agentNode(
        'implement',
        AgentSessionState.waiting,
        NodeStatus.Waiting,
        'Choose an implementation'
      ),
    ]);

    const tablist = screen.getByRole('tablist', {
      name: 'Agent conversations',
    });
    expect(
      within(tablist).getByRole('tab', { name: /analyze/ })
    ).toHaveAttribute('aria-selected', 'false');
    expect(
      within(tablist).getByRole('tab', { name: /implement/ })
    ).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText('Choose an implementation')).toBeInTheDocument();
    expect(screen.queryByText('Analysis complete')).not.toBeInTheDocument();
  });

  it('switches conversations with pointer and keyboard input', () => {
    renderAgentSessions([
      agentNode(
        'analyze',
        AgentSessionState.succeeded,
        NodeStatus.Success,
        'Analysis complete'
      ),
      agentNode(
        'implement',
        AgentSessionState.running,
        NodeStatus.Running,
        'Implementation running'
      ),
    ]);

    const analyzeTab = screen.getByRole('tab', { name: /analyze/ });
    const implementTab = screen.getByRole('tab', { name: /implement/ });
    fireEvent.click(analyzeTab);
    expect(screen.getByText('Analysis complete')).toBeInTheDocument();
    expect(
      screen.queryByText('Implementation running')
    ).not.toBeInTheDocument();

    fireEvent.keyDown(analyzeTab, { key: 'ArrowRight' });
    expect(implementTab).toHaveAttribute('aria-selected', 'true');
    expect(implementTab).toHaveFocus();
    expect(screen.getByText('Implementation running')).toBeInTheDocument();

    fireEvent.keyDown(implementTab, { key: 'Home' });
    expect(analyzeTab).toHaveAttribute('aria-selected', 'true');
    expect(analyzeTab).toHaveFocus();

    fireEvent.keyDown(analyzeTab, { key: 'End' });
    expect(implementTab).toHaveAttribute('aria-selected', 'true');
    expect(implementTab).toHaveFocus();
  });

  it('preserves selection when polling adds a waiting session', () => {
    const analyze = agentNode(
      'analyze',
      AgentSessionState.succeeded,
      NodeStatus.Success,
      'Analysis complete'
    );
    const implement = agentNode(
      'implement',
      AgentSessionState.running,
      NodeStatus.Running,
      'Implementation running'
    );
    const { rerender } = renderAgentSessions([analyze, implement]);

    fireEvent.click(screen.getByRole('tab', { name: /analyze/ }));
    rerender(
      <AgentSessionHarness
        nodes={[
          analyze,
          implement,
          agentNode(
            'review',
            AgentSessionState.waiting,
            NodeStatus.Waiting,
            'Review needs input'
          ),
        ]}
      />
    );

    expect(screen.getByRole('tab', { name: /analyze/ })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByRole('tab', { name: /review/ })).toHaveAttribute(
      'aria-selected',
      'false'
    );
    expect(screen.getByText('Analysis complete')).toBeInTheDocument();
    expect(screen.queryByText('Review needs input')).not.toBeInTheDocument();
  });

  it('falls back when the selected session disappears', () => {
    const analyze = agentNode(
      'analyze',
      AgentSessionState.succeeded,
      NodeStatus.Success,
      'Analysis complete'
    );
    const implement = agentNode(
      'implement',
      AgentSessionState.running,
      NodeStatus.Running,
      'Implementation running'
    );
    const { rerender } = renderAgentSessions([analyze, implement]);

    fireEvent.click(screen.getByRole('tab', { name: /analyze/ }));
    rerender(<AgentSessionHarness nodes={[implement]} />);

    expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
    expect(screen.getByText('Implementation running')).toBeInTheDocument();
  });

  it('shows a browser question with screenshot thumbnails', async () => {
    const GET = vi.fn().mockResolvedValue({
      data: new Blob(['png'], { type: 'image/png' }),
      response: new Response(),
    });
    useClientMock.mockReturnValue({ GET, POST: vi.fn() } as never);
    const createObjectURL = vi.fn(() => 'blob:screenshot');
    Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() });

    const login = {
      step: { name: 'login' },
      status: NodeStatus.Waiting,
      agentSession: {
        provider: 'browser',
        state: AgentSessionState.waiting,
        events: [
          {
            sequence: 1,
            id: 'browser-1-1',
            type: 'tool',
            name: 'goto',
            content: 'https://portal.example.com',
            files: ['browser/login/01-goto.png'],
          },
        ],
        interactions: [
          {
            id: 'ask-1-1',
            kind: 'question',
            status: 'pending',
            questions: [
              {
                header: 'Browser input',
                question: 'Enter the code sent to your phone',
                custom: true,
              },
            ],
          },
        ],
      },
    };
    renderAgentSessions([login as never]);

    expect(
      screen.getByText('The browser step needs an answer')
    ).toBeInTheDocument();
    expect(
      screen.getByText('Enter the code sent to your phone')
    ).toBeInTheDocument();
    expect(screen.getByText('Browser session')).toBeInTheDocument();
    expect(
      await screen.findByRole('img', { name: '01-goto.png' })
    ).toHaveAttribute('src', 'blob:screenshot');
    expect(GET).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/artifacts/download',
      expect.objectContaining({
        params: {
          path: { name: 'agent-workflow', dagRunId: 'run-1' },
          query: { remoteNode: 'local', path: 'browser/login/01-goto.png' },
        },
      })
    );

    fireEvent.click(
      screen.getByRole('button', { name: /Start clean session/ })
    );
    expect(
      screen.getByText('Start this browser step over?')
    ).toBeInTheDocument();
  });

  it('does not offer a restart for providers that cannot restart', () => {
    const other = agentNode(
      'review',
      AgentSessionState.failed,
      NodeStatus.Failed
    );
    other.agentSession.provider = 'other';
    renderAgentSessions([other]);

    expect(
      screen.queryByRole('button', { name: /Start clean session/ })
    ).not.toBeInTheDocument();
  });
});
