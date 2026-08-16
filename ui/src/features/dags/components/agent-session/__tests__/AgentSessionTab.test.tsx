// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
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

function dagRun(state: AgentSessionState, status: NodeStatus) {
  return {
    dagRunId: 'run-1',
    name: 'agent-workflow',
    rootDAGRunId: 'run-1',
    rootDAGRunName: 'agent-workflow',
    nodes: [
      {
        step: { name: 'implement' },
        status,
        agentSession: {
          provider: 'opencode',
          state,
          lastError: sessionError,
          events: [],
        },
      },
    ],
  } as never;
}

describe('AgentSessionTab', () => {
  beforeEach(() => {
    useClientMock.mockReturnValue({ POST: vi.fn() } as never);
  });

  it('shows the provider error when the session failed', () => {
    render(
      <AgentSessionTab
        dagRun={dagRun(AgentSessionState.failed, NodeStatus.Failed)}
        onChanged={vi.fn()}
      />
    );

    expect(screen.getByText(sessionError)).toBeInTheDocument();
  });

  it('does not show a stale provider error after the session succeeds', () => {
    render(
      <AgentSessionTab
        dagRun={dagRun(AgentSessionState.succeeded, NodeStatus.Success)}
        onChanged={vi.fn()}
      />
    );

    expect(screen.queryByText(sessionError)).not.toBeInTheDocument();
  });
});
