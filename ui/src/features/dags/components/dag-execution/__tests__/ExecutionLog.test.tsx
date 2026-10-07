// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ActivityLine } from '../ActivityLine';
import StepLog from '../StepLog';
import ExecutionLog from '../ExecutionLog';
import LogPageSizeSelect from '../LogPageSizeSelect';
import { downloadFromUrl, downloadFromForm } from '@/lib/download';
import { NodeStatus, Stream } from '@/api/v1/schema';
import type { StepLogSSEResponse } from '@/hooks/useStepLogSSE';
import { UserPreferencesProvider } from '@/contexts/UserPreference';

const showToast = vi.hoisted(() => vi.fn());
vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast }),
}));

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
  queries: [] as Array<{
    tail?: number;
    head?: number;
    offset?: number;
    limit?: number;
  }>,
  paths: [] as string[],
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ apiURL: '/api/v1' }),
}));
vi.mock('@/lib/download', () => ({
  downloadFromUrl: vi.fn(),
  downloadFromForm: vi.fn(),
  downloadBlob: vi.fn(),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({ useRemoteNode: () => 'edge' }));
vi.mock('@/hooks/useDAGRunLogsSSE', () => ({
  useDAGRunLogsSSE: () => ({
    data: null,
    isConnected: false,
    shouldUseFallback: true,
  }),
}));
vi.mock('@/hooks/api', () => ({
  useQuery: (
    path: string,
    options?: {
      params: {
        query: { head?: number; tail?: number; offset?: number; limit?: number };
      };
    } | null
  ) => {
    if (options?.params?.query) {
      logs.queries.push(options.params.query);
      logs.paths.push(path);
    }
    return {
      data: options?.params.query.head && logs.head ? logs.head : logs.data,
      mutate: logs.mutate,
      isLoading: false,
    };
  },
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
  vi.mocked(downloadFromUrl).mockReset().mockResolvedValue(undefined);
  vi.mocked(downloadFromForm).mockReset();
  showToast.mockReset();
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
  logs.queries = [];
  logs.paths = [];
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
  it('reads and downloads a foreach body step log through the foreach route', async () => {
    render(
      <StepLog
        dagName="example"
        dagRunId="run"
        stepName="body"
        stream={Stream.stderr}
        foreach={{ stepName: 'each', item: '2.inner.0' }}
        node={{ status: NodeStatus.Failed } as never}
      />,
      { wrapper: UserPreferencesProvider }
    );
    expect(new Set(logs.paths)).toEqual(
      new Set([
        '/dag-runs/{name}/{dagRunId}/steps/{stepName}/foreach/{item}/steps/{bodyStepName}/log',
      ])
    );
    expect(screen.getByText('first output')).toBeVisible();

    fireEvent.click(screen.getByTitle('Download full log'));
    await vi.waitFor(() => expect(downloadFromUrl).toHaveBeenCalled());
    const [url, filename] = vi.mocked(downloadFromUrl).mock.calls[0]!;
    expect(url).toContain(
      '/api/v1/dag-runs/example/run/steps/each/foreach/2.inner.0/steps/body/log/download'
    );
    expect(url).toContain('stream=stderr');
    expect(filename).toBe('example-run-each-2.inner.0-body-stderr.log');
  });

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

describe('ExecutionLog ZIP download', () => {
  it.each([
    {
      label: 'root run',
      dagRun: undefined,
      path: '/dag-runs/example/run/steps/log/download',
    },
    {
      label: 'child run',
      dagRun: {
        rootDAGRunName: 'parent',
        rootDAGRunId: 'root',
        dagRunId: 'run',
      } as never,
      path: '/dag-runs/parent/root/sub-dag-runs/run/steps/log/download',
    },
  ])(
    'downloads the selected $label from its remote node',
    ({ dagRun, path }) => {
      render(<ExecutionLog name="example" dagRunId="run" dagRun={dagRun} />, {
        wrapper: UserPreferencesProvider,
      });
      fireEvent.click(
        screen.getByRole('button', { name: 'Download step logs (ZIP)' })
      );
      expect(downloadFromForm).toHaveBeenCalledWith(
        `${window.location.origin}/api/v1${path}?remoteNode=edge`
      );
      expect(showToast).toHaveBeenCalledWith(
        'Download requested. Check your browser downloads.',
        { variant: 'info' }
      );
    }
  );
});

describe('log pagination controls', () => {
  const pagedData = {
    content: 'first line',
    totalLines: 50000,
    lineCount: 1,
    hasMore: true,
  };

  it('requests a custom page size for the run log', () => {
    logs.data = { ...pagedData };
    render(<ExecutionLog name="example" dagRunId="run" />, {
      wrapper: UserPreferencesProvider,
    });
    fireEvent.change(screen.getByLabelText('Lines per page'), {
      target: { value: 'custom' },
    });
    const input = screen.getByLabelText('Custom lines per page');
    fireEvent.change(input, { target: { value: '2345' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(logs.queries[logs.queries.length - 1]).toMatchObject({ tail: 2345 });
  });

  it('clamps a custom page size to the bounded maximum', () => {
    logs.data = { ...pagedData };
    render(<ExecutionLog name="example" dagRunId="run" />, {
      wrapper: UserPreferencesProvider,
    });
    fireEvent.change(screen.getByLabelText('Lines per page'), {
      target: { value: 'custom' },
    });
    const input = screen.getByLabelText('Custom lines per page');
    fireEvent.change(input, { target: { value: '500000' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(logs.queries[logs.queries.length - 1]).toMatchObject({
      tail: 10000,
    });
    expect(input).toHaveValue(10000);
  });

  it('requests the maximum preset size', () => {
    logs.data = { ...pagedData };
    render(<ExecutionLog name="example" dagRunId="run" />, {
      wrapper: UserPreferencesProvider,
    });
    fireEvent.change(screen.getByLabelText('Lines per page'), {
      target: { value: '10000' },
    });
    expect(logs.queries[logs.queries.length - 1]).toMatchObject({
      tail: 10000,
    });
  });

  it('jumps to a specific page of the run log', () => {
    // The view-mode switch locks navigation briefly; advance past the timeout.
    vi.useFakeTimers();
    try {
      logs.data = { ...pagedData };
      render(<ExecutionLog name="example" dagRunId="run" />, {
        wrapper: UserPreferencesProvider,
      });
      fireEvent.click(screen.getByRole('button', { name: 'Page View' }));
      act(() => {
        vi.advanceTimersByTime(3100);
      });
      const pageInput = screen.getByLabelText('Page');
      fireEvent.change(pageInput, { target: { value: '4' } });
      fireEvent.keyDown(pageInput, { key: 'Enter' });
      expect(logs.queries[logs.queries.length - 1]).toMatchObject({ offset: 3001, limit: 1000 });
    } finally {
      vi.useRealTimers();
    }
  });

  it('requests a custom page size for the step log', () => {
    logs.data = { ...pagedData };
    render(<StepLog dagName="example" dagRunId="run" stepName="build" />, {
      wrapper: UserPreferencesProvider,
    });
    fireEvent.change(screen.getByLabelText('Lines per page'), {
      target: { value: 'custom' },
    });
    const input = screen.getByLabelText('Custom lines per page');
    fireEvent.change(input, { target: { value: '2345' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(logs.queries[logs.queries.length - 1]).toMatchObject({ tail: 2345 });
  });

  it('jumps to a specific page of the step log', () => {
    // The view-mode switch locks navigation briefly; advance past the timeout.
    vi.useFakeTimers();
    try {
      logs.data = { ...pagedData };
      render(<StepLog dagName="example" dagRunId="run" stepName="build" />, {
        wrapper: UserPreferencesProvider,
      });
      fireEvent.click(screen.getByRole('button', { name: 'Page View' }));
      act(() => {
        vi.advanceTimersByTime(3100);
      });
      const pageInput = screen.getByLabelText('Page');
      fireEvent.change(pageInput, { target: { value: '3' } });
      fireEvent.keyDown(pageInput, { key: 'Enter' });
      expect(logs.queries[logs.queries.length - 1]).toMatchObject({ offset: 2001, limit: 1000 });
    } finally {
      vi.useRealTimers();
    }
  });

  describe.each(['run', 'step'])('%s log page input', (log) => {
    it.each([
      { entered: '-5', page: 1 },
      { entered: '1.9', page: 1 },
      { entered: '999', page: 50 },
    ])('normalizes $entered to page $page', ({ entered, page }) => {
      vi.useFakeTimers();
      try {
        logs.data = { ...pagedData };
        render(
          log === 'run' ? (
            <ExecutionLog name="example" dagRunId="run" />
          ) : (
            <StepLog dagName="example" dagRunId="run" stepName="build" />
          ),
          { wrapper: UserPreferencesProvider }
        );
        fireEvent.click(screen.getByRole('button', { name: 'Page View' }));
        act(() => vi.advanceTimersByTime(3100));
        const input = screen.getByLabelText('Page');
        if (page !== 1) {
          fireEvent.change(input, { target: { value: String(page) } });
          fireEvent.keyDown(input, { key: 'Enter' });
          act(() => vi.advanceTimersByTime(3100));
        }

        // Normalization must also apply when the selected page stays the same.
        fireEvent.change(input, { target: { value: entered } });
        fireEvent.keyDown(input, { key: 'Enter' });

        expect(input).toHaveValue(page);
        expect(logs.queries[logs.queries.length - 1]).toMatchObject({
          offset: (page - 1) * 1000 + 1,
          limit: 1000,
        });
      } finally {
        vi.useRealTimers();
      }
    });
  });

  it('restores the custom size when step log options reopen', () => {
    logs.data = { ...pagedData };
    render(
      <StepLog
        dagName="example"
        dagRunId="run"
        stepName="build"
        followTail={true}
      />,
      { wrapper: UserPreferencesProvider }
    );
    const options = screen.getByRole('button', { name: 'Log options' });
    fireEvent.click(options);
    fireEvent.change(screen.getByLabelText('Lines per page'), {
      target: { value: 'custom' },
    });
    const input = screen.getByLabelText('Custom lines per page');
    fireEvent.change(input, { target: { value: '1234' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.click(options);
    fireEvent.click(options);

    expect(logs.queries[logs.queries.length - 1]).toMatchObject({ tail: 1234 });
    expect(screen.getByLabelText('Lines per page')).toHaveValue('custom');
    expect(screen.getByLabelText('Custom lines per page')).toHaveValue(1234);
  });

  it('keeps the page when an unchanged custom size loses focus', () => {
    vi.useFakeTimers();
    try {
      logs.data = { ...pagedData };
      render(<StepLog dagName="example" dagRunId="run" stepName="build" />, {
        wrapper: UserPreferencesProvider,
      });
      fireEvent.change(screen.getByLabelText('Lines per page'), {
        target: { value: 'custom' },
      });
      const sizeInput = screen.getByLabelText('Custom lines per page');
      fireEvent.change(sizeInput, { target: { value: '1234' } });
      fireEvent.keyDown(sizeInput, { key: 'Enter' });
      act(() => vi.advanceTimersByTime(3100));
      fireEvent.click(screen.getByRole('button', { name: 'Page View' }));
      act(() => vi.advanceTimersByTime(3100));
      const pageInput = screen.getByLabelText('Page');
      fireEvent.change(pageInput, { target: { value: '4' } });
      fireEvent.keyDown(pageInput, { key: 'Enter' });
      act(() => vi.advanceTimersByTime(3100));
      fireEvent.blur(sizeInput);

      expect(pageInput).toHaveValue(4);
      expect(logs.queries[logs.queries.length - 1]).toMatchObject({
        offset: 3703,
        limit: 1234,
      });
    } finally {
      vi.useRealTimers();
    }
  });

  it.each([
    { entered: '-5', applied: 1 },
    { entered: '1234.8', applied: 1234 },
  ])('normalizes custom input $entered to $applied', ({ entered, applied }) => {
    const onPageSizeChange = vi.fn();
    render(
      <LogPageSizeSelect pageSize={1000} onPageSizeChange={onPageSizeChange} />
    );
    fireEvent.change(screen.getByLabelText('Lines per page'), {
      target: { value: 'custom' },
    });
    const input = screen.getByLabelText('Custom lines per page');
    fireEvent.change(input, { target: { value: entered } });
    fireEvent.blur(input);

    expect(onPageSizeChange).toHaveBeenCalledWith(applied);
    expect(input).toHaveValue(applied);
  });
});

describe('ExecutionLog download feedback', () => {
  it.each([false, true])(
    'releases the button after submitting (failure: %s)',
    (fail) => {
      render(<ExecutionLog name="example" dagRunId="run" />, {
        wrapper: UserPreferencesProvider,
      });
      const button = screen.getByRole('button', {
        name: 'Download step logs (ZIP)',
      });
      vi.mocked(downloadFromForm).mockImplementation(() => {
        expect(button).toBeDisabled();
        if (fail) {
          throw new Error('Cannot submit');
        }
      });
      fireEvent.click(button);
      expect(button).toBeEnabled();
      if (fail) {
        expect(showToast).toHaveBeenCalledWith('Cannot submit', {
          variant: 'error',
        });
      }
      fireEvent.click(button);
      expect(downloadFromForm).toHaveBeenCalledTimes(2);
    }
  );
});
