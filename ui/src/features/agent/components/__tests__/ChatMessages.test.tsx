// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { ChatMessages } from '../ChatMessages';
import type { Message } from '../../types';

function renderMessages(messages: Message[]) {
  return render(
    <UserPreferencesProvider>
      <ChatMessages
        messages={messages}
        pendingUserMessage={null}
        isWorking={false}
      />
    </UserPreferencesProvider>
  );
}

function patchMessage(id: string, callId: string, args: Record<string, unknown>): Message {
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
          name: 'patch',
          arguments: JSON.stringify(args),
        },
      },
    ],
    created_at: '2026-05-07T00:00:00Z',
  };
}

function toolResultMessage(id: string, callId: string, isError: boolean): Message {
  return {
    id,
    session_id: 'session-1',
    type: 'user',
    sequence_id: Number(id.replace(/\D/g, '')) || 1,
    content: '',
    tool_results: [
      {
        tool_call_id: callId,
        content: isError
          ? 'old_string not found in file. Make sure to include exact text including whitespace and indentation.'
          : 'Replaced 1 lines with 2 lines in /tmp/MEMORY.md',
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

function openPatch(index = 0) {
  fireEvent.click(screen.getAllByRole('button', { name: /patch/i })[index]!);
}

describe('ChatMessages patch tool status', () => {
  it('keeps patch tool calls collapsed by default', () => {
    renderMessages([patchMessage('msg-1', 'call-1', replaceArgs)]);

    expect(screen.getByRole('button', { name: /patch\[\+\]/i })).toBeInTheDocument();
    expect(screen.queryByText('Proposed patch')).not.toBeInTheDocument();
  });

  it('labels a patch call without a result as proposed', () => {
    renderMessages([patchMessage('msg-1', 'call-1', replaceArgs)]);
    openPatch();

    expect(screen.getByText('Proposed patch')).toBeInTheDocument();
    expect(screen.queryByText('Patch failed')).not.toBeInTheDocument();
    expect(screen.queryByText('Patch applied')).not.toBeInTheDocument();
  });

  it('marks a matching failed tool result on the patch preview', () => {
    renderMessages([
      patchMessage('msg-1', 'call-1', replaceArgs),
      toolResultMessage('msg-2', 'call-1', true),
    ]);
    openPatch();

    expect(screen.getByText('Patch failed')).toBeInTheDocument();
    expect(screen.queryByText('Proposed patch')).not.toBeInTheDocument();
  });

  it('marks a matching successful tool result on the patch preview', () => {
    renderMessages([
      patchMessage('msg-1', 'call-1', replaceArgs),
      toolResultMessage('msg-2', 'call-1', false),
    ]);
    openPatch();

    expect(screen.getByText('Patch applied')).toBeInTheDocument();
    expect(screen.queryByText('Proposed patch')).not.toBeInTheDocument();
  });

  it('keeps a failed first patch visibly failed when a later patch succeeds', () => {
    renderMessages([
      patchMessage('msg-1', 'call-1', replaceArgs),
      toolResultMessage('msg-2', 'call-1', true),
      patchMessage('msg-3', 'call-2', {
        path: '/tmp/MEMORY.md',
        operation: 'replace',
        old_string: 'line two\n',
        new_string: 'line three\n',
      }),
      toolResultMessage('msg-4', 'call-2', false),
    ]);
    openPatch(0);
    openPatch(1);

    expect(screen.getByText('Patch failed')).toBeInTheDocument();
    expect(screen.getByText('Patch applied')).toBeInTheDocument();
  });
});
