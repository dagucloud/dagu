// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ActivityLine } from '../ActivityLine';
import StepLog from '../StepLog';
import { NodeStatus, Stream } from '@/api/v1/schema';
import type { StepLogSSEResponse } from '@/hooks/useStepLogSSE';
import { UserPreferencesProvider } from '@/contexts/UserPreference';

const logs = vi.hoisted(() => ({
  data: {
    content: 'first output',
    totalLines: 1,
    lineCount: 1,
    hasMore: false,
  },
  mutate: vi.fn(),
  head: undefined as { content: string } | undefined,
  sse: null as StepLogSSEResponse | null,
  connected: false,
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ apiURL: '/api/v1' }),
}));
vi.mock('@/hooks/api', () => ({
  useQuery: (
    _path: string,
    options?: { params: { query: { head?: number } } }
  ) => ({
    data: options?.params.query.head && logs.head ? logs.head : logs.data,
    mutate: logs.mutate,
    isLoading: false,
  }),
}));
vi.mock('@/hooks/useStepLogSSE', () => ({
  useStepLogSSE: () => ({
    data: logs.sse,
    isConnected: logs.connected,
    isConnecting: false,
    shouldUseFallback: !logs.connected,
    error: null,
  }),
}));

beforeEach(() => {
  logs.data = {
    content: 'first output',
    totalLines: 1,
    lineCount: 1,
    hasMore: false,
  };
  logs.mutate.mockReset().mockResolvedValue(undefined);
  logs.head = undefined;
  logs.sse = null;
  logs.connected = false;
});

describe('ActivityLine', () => {
  it('exposes its source line for jump navigation and wraps messages', () => {
    const { container, rerender } = render(
      <ActivityLine
        line={{
          timestamp: '2026-08-06T12:00:00Z',
          level: 'INFO',
          message: 'structured-message',
          structured: true,
        }}
        lineNumber={42}
      />
    );

    expect(container.firstChild).toHaveAttribute('data-line-number', '42');
    expect(screen.getByText('structured-message').parentElement).toHaveClass(
      'whitespace-normal',
      'break-words'
    );

    rerender(
      <ActivityLine
        line={{ message: 'plain-message', structured: false }}
        lineNumber={43}
      />
    );

    expect(container.firstChild).toHaveAttribute('data-line-number', '43');
    expect(container.firstChild).toHaveClass(
      'whitespace-normal',
      'break-words'
    );
  });
});

describe('StepLog', () => {
  it('shows the beginning of an active log when requested', async () => {
    const props = {
      dagName: 'example',
      dagRunId: 'run',
      stepName: 'build',
      node: { status: NodeStatus.Running } as never,
    };
    const view = render(<StepLog {...props} />, {
      wrapper: UserPreferencesProvider,
    });
    fireEvent.click(screen.getByRole('button', { name: 'Show Beginning' }));
    logs.head = { content: 'beginning output' };
    view.rerender(<StepLog {...props} />);
    expect(await screen.findByText('beginning output')).toBeVisible();
  });

  it('shows line counts for the selected stream', () => {
    logs.connected = true;
    logs.sse = {
      stdoutContent: 'streamed output',
      stderrContent: 'error output',
      lineCount: 1000,
      totalLines: 2000,
      hasMore: true,
    };
    logs.data = {
      content: 'error output',
      lineCount: 1,
      totalLines: 1,
      hasMore: false,
    };
    render(
      <StepLog
        dagName="example"
        dagRunId="run"
        stepName="build"
        node={{ status: NodeStatus.Running } as never}
        stream={Stream.stderr}
      />,
      {
        wrapper: UserPreferencesProvider,
      }
    );
    expect(screen.getByText('Showing 1 of 1 lines')).toBeVisible();
  });

  it('retains streamed output while reconnecting and accepts fresh REST output', () => {
    logs.connected = true;
    logs.sse = {
      stdoutContent: 'streamed output',
      stderrContent: 'streamed error',
      lineCount: 1,
      totalLines: 1,
      hasMore: false,
    };
    const props = {
      dagName: 'example',
      dagRunId: 'run',
      stepName: 'build',
      node: { status: NodeStatus.Running } as never,
    };
    const view = render(<StepLog {...props} />, {
      wrapper: UserPreferencesProvider,
    });
    expect(screen.getByText('streamed output')).toBeVisible();
    logs.connected = false;
    view.rerender(<StepLog {...props} />);
    expect(screen.getByText('streamed output')).toBeVisible();
    logs.data = { ...logs.data, content: 'reconnected output' };
    view.rerender(<StepLog {...props} />);
    expect(screen.getByText('reconnected output')).toBeVisible();
  });

  it('opens a newly selected stream at its latest output', () => {
    const props = {
      dagName: 'example',
      dagRunId: 'run',
      stepName: 'build',
      node: { status: NodeStatus.Running } as never,
    };
    const view = render(<StepLog {...props} />, {
      wrapper: UserPreferencesProvider,
    });
    fireEvent.focus(screen.getByPlaceholderText('Search in loaded lines...'));
    logs.data = { ...logs.data, content: 'stderr output' };
    view.rerender(<StepLog {...props} stream={Stream.stderr} />);
    expect(screen.getByText('stderr output')).toBeVisible();
    expect(screen.getByRole('button', { name: 'LIVE' })).toBeVisible();
  });
  it('keeps the displayed output while reading older lines', async () => {
    const props = {
      dagName: 'example',
      dagRunId: 'run',
      stepName: 'build',
      node: { status: NodeStatus.Running } as never,
    };
    const view = render(<StepLog {...props} />, {
      wrapper: UserPreferencesProvider,
    });
    const content = screen
      .getByText('first output')
      .closest('pre')!.parentElement!;
    Object.defineProperties(content, {
      scrollHeight: { configurable: true, value: 1000 },
      clientHeight: { configurable: true, value: 200 },
    });
    fireEvent.wheel(content, { deltaY: -100 });
    fireEvent.scroll(content, { target: { scrollTop: 100 } });
    logs.data = { ...logs.data, content: 'latest output' };
    view.rerender(<StepLog {...props} />);
    expect(screen.getByText('first output')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Back to live' }));
    expect(await screen.findByText('latest output')).toBeVisible();
  });

  it('loads the requested tail size while reading older output', () => {
    const props = { dagName: 'example', dagRunId: 'run', stepName: 'build' };
    const view = render(<StepLog {...props} />, {
      wrapper: UserPreferencesProvider,
    });
    fireEvent.focus(screen.getByPlaceholderText('Search in loaded lines...'));
    logs.data = { ...logs.data, content: 'requested tail' };
    view.rerender(<StepLog {...props} />);
    fireEvent.change(screen.getByLabelText('Lines per page'), {
      target: { value: '100' },
    });
    expect(screen.getByText('requested tail')).toBeVisible();
  });

  it.each([NodeStatus.Running, NodeStatus.Success])(
    'delivers final output when opened with status %s',
    async (initialStatus) => {
      const props = { dagName: 'example', dagRunId: 'run', stepName: 'build' };
      const onSettled = vi.fn();
      const view = render(
        <StepLog
          {...props}
          node={{ status: initialStatus } as never}
          onSettled={onSettled}
        />,
        {
          wrapper: UserPreferencesProvider,
        }
      );
      view.rerender(
        <StepLog
          {...props}
          node={{ status: NodeStatus.Success } as never}
          onSettled={onSettled}
        />
      );
      await vi.waitFor(() => expect(onSettled).toHaveBeenCalledWith('build'));
    }
  );
});
