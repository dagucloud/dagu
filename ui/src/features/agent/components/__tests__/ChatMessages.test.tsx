// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { ChatMessages } from '../ChatMessages';
import type { Message } from '../../types';

function renderMessages(
  messages: Message[],
  options?: {
    isWorking?: boolean;
  }
) {
  return render(
    <UserPreferencesProvider>
      <ChatMessages
        messages={messages}
        pendingUserMessage={null}
        isWorking={options?.isWorking ?? false}
      />
    </UserPreferencesProvider>
  );
}

function toolCallMessage(
  id: string,
  callId: string,
  toolName: string,
  args: Record<string, unknown>
): Message {
  return {
    id,
    session_id: 'session-1',
    type: 'assistant',
    sequence_id: Number(id.replace(/\D/g, '')) || 1,
    content: '',
    tool_calls: [
      {
        id: callId,
        type: 'function',
        function: {
          name: toolName,
          arguments: JSON.stringify(args),
        },
      },
    ],
    created_at: '2026-05-07T00:00:00Z',
  };
}

function patchMessage(
  id: string,
  callId: string,
  args: Record<string, unknown>
): Message {
  return toolCallMessage(id, callId, 'patch', args);
}

function toolResultMessage(
  id: string,
  callId: string,
  isError: boolean,
  content?: string
): Message {
  return {
    id,
    session_id: 'session-1',
    type: 'user',
    sequence_id: Number(id.replace(/\D/g, '')) || 1,
    content: '',
    tool_results: [
      {
        tool_call_id: callId,
        content:
          content ??
          (isError
            ? 'old_string not found in file. Make sure to include exact text including whitespace and indentation.'
            : 'Replaced 1 lines with 2 lines in /tmp/MEMORY.md'),
        is_error: isError,
      },
    ],
    created_at: '2026-05-07T00:00:01Z',
  };
}

const replaceArgs = {
  path: '/tmp/MEMORY.md',
  operation: 'replace',
  old_string: 'line one\n',
  new_string: 'line one\nline two\n',
};

function openTool(name: RegExp | string, index = 0) {
  fireEvent.click(screen.getAllByRole('button', { name })[index]!);
}

describe('ChatMessages tool activity', () => {
  it('renders tool calls as a quiet short display label', () => {
    renderMessages([patchMessage('msg-1', 'call-1', replaceArgs)]);

    expect(
      screen.getByRole('button', { name: /edit DAG/i })
    ).toBeInTheDocument();
    expect(screen.queryByText('Proposed patch')).not.toBeInTheDocument();
    expect(screen.queryByText('Patch failed')).not.toBeInTheDocument();
    expect(screen.queryByText('Patch applied')).not.toBeInTheDocument();
  });

  it('does not render assistant token usage metadata', () => {
    renderMessages([
      {
        id: 'msg-1',
        session_id: 'session-1',
        type: 'assistant',
        sequence_id: 1,
        content: 'Done.',
        usage: {
          prompt_tokens: 10,
          completion_tokens: 5,
          total_tokens: 15,
        },
        cost: 0.01,
        created_at: '2026-05-07T00:00:00Z',
      },
    ]);

    expect(screen.getByText('Done.')).toBeInTheDocument();
    expect(screen.queryByText(/15 tokens/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/\$0\.01/)).not.toBeInTheDocument();
  });

  it('renders chat errors as raw developer text', () => {
    const { container } = renderMessages([
      {
        id: 'msg-1',
        session_id: 'session-1',
        type: 'error',
        sequence_id: 1,
        content:
          "LLM request failed: openai-codex API error (status 400): Invalid schema for function 'patch'",
        created_at: '2026-05-07T00:00:00Z',
      },
    ]);

    expect(screen.getByText(/LLM request failed/i)).toBeInTheDocument();
    expect(container.textContent).not.toContain('ERROR:');
    expect(container.querySelector('[role="alert"]')).toBeNull();
  });

  it('renders the working state as a quiet rotating indicator', () => {
    const { container } = renderMessages(
      [
        {
          id: 'msg-1',
          session_id: 'session-1',
          type: 'assistant',
          sequence_id: 1,
          content: 'Creating the DAG.',
          created_at: '2026-05-07T00:00:00Z',
        },
      ],
      { isWorking: true }
    );

    const status = screen.getByRole('status', {
      name: 'Agent response in progress',
    });

    expect(status).toBeInTheDocument();
    expect(status.textContent).toBe('');
    expect(screen.queryByText(/Agent is working/i)).not.toBeInTheDocument();
    expect(container.querySelector('.animate-spin')).not.toBeNull();
    expect(container.querySelector('.animate-pulse')).toBeNull();
  });

  it('shows the rotating indicator while waiting for the first message', () => {
    renderMessages([], { isWorking: true });

    const status = screen.getByRole('status', {
      name: 'Agent response in progress',
    });

    expect(status.querySelector('.animate-spin')).not.toBeNull();
    expect(screen.queryByText('Agent session ready')).not.toBeInTheDocument();
  });

  it('uses action-specific short labels for tools', () => {
    renderMessages([
      patchMessage('msg-1', 'call-1', {
        path: '/tmp/sample.yaml',
        operation: 'create',
        content: 'steps: []\n',
      }),
      toolCallMessage('msg-2', 'call-2', 'dag_def_manage', {
        action: 'schema',
        path: 'steps',
      }),
    ]);

    expect(
      screen.getByRole('button', { name: /create DAG/i })
    ).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: /inspect DAG/i })
    ).toBeInTheDocument();
  });

  it('uses short labels for web tools', () => {
    renderMessages([
      toolCallMessage('msg-1', 'call-1', 'web_search', {
        query: 'Dagu latest release',
        limit: 5,
      }),
      toolCallMessage('msg-2', 'call-2', 'web_extract', {
        urls: ['https://docs.dagu.sh'],
      }),
    ]);

    expect(
      screen.getByRole('button', { name: /search web/i })
    ).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: /read web page/i })
    ).toBeInTheDocument();
  });

  it('does not display paired tool result text or status', () => {
    const longResult =
      'patch failed while creating a sample DAG. '.repeat(4) +
      'The create call included a non-empty old_string.';

    renderMessages([
      patchMessage('msg-1', 'call-1', replaceArgs),
      toolResultMessage('msg-2', 'call-1', true, longResult),
    ]);

    expect(screen.getAllByRole('button', { name: /edit DAG/i })).toHaveLength(
      1
    );
    expect(
      screen.queryByText(/non-empty old_string/i)
    ).not.toBeInTheDocument();
    expect(screen.queryByText('Failed')).not.toBeInTheDocument();
    expect(screen.queryByText('Done')).not.toBeInTheDocument();
    expect(screen.queryByText('Patch failed')).not.toBeInTheDocument();

    openTool(/edit DAG/i);

    expect(screen.getByText(/old_string/i)).toBeInTheDocument();
    expect(
      screen.queryByText(/non-empty old_string/i)
    ).not.toBeInTheDocument();
  });

  it('still shows unpaired tool results', () => {
    renderMessages([
      toolResultMessage(
        'msg-1',
        'missing-call',
        false,
        'Standalone tool result'
      ),
    ]);

    expect(screen.getByText('Standalone tool result')).toBeInTheDocument();
  });

  it('does not render navigation ui_action messages', () => {
    renderMessages([
      {
        id: 'msg-1',
        session_id: 'session-1',
        type: 'ui_action',
        sequence_id: 1,
        ui_action: {
          type: 'navigate',
          path: '/dags/sample_parallel_report.yaml',
        },
        created_at: '2026-05-07T00:00:00Z',
      },
    ]);

    expect(screen.queryByText('Navigation')).not.toBeInTheDocument();
    expect(screen.queryByText(/Navigating to/i)).not.toBeInTheDocument();
  });
});
