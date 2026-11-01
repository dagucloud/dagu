// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ActivityLine } from '../ActivityLine';
import StepLog from '../StepLog';
import ExecutionLog from '../ExecutionLog';
import LogPageSizeSelect from '../LogPageSizeSelect';
import { downloadFromUrl, downloadFromForm } from '@/lib/download';
import { NodeStatus, Status, Stream } from '@/api/v1/schema';
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
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'edge',
}));
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
        query: {
          head?: number;
          tail?: number;
          offset?: number;
          limit?: number;
        };
      };
    } | null
  ) => {
    if (options?.params?.query) {
      logs.queries.push(options.params.query);
      logs.paths.push(path);
    }
    return {
      data: options?.params.query.offset && logs.head ? logs.head : logs.data,
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

  // The details word is gone: a chevron on the line of the message opens
  // them, and the whole line is the target.
  it('opens the details with a chevron on the line, not a Details word', () => {
    const { container } = render(
      <ActivityLine
        line={{
          timestamp: '2026-08-06T12:00:00Z',
          level: 'INFO',
          step: 'build',
          message: 'step finished',
          details: 'exit status 0',
          structured: true,
        }}
        lineNumber={1}
      />
    );

    const toggle = screen.getByRole('button', { name: 'Details' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(toggle).toHaveTextContent('');
    expect(screen.queryByText('exit status 0')).toBeNull();
    // The button is on the line of the message.
    expect(
      screen.getByText('step finished').parentElement?.parentElement
    ).toContainElement(toggle);

    fireEvent.click(container.firstChild as Element);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('exit status 0')).toBeVisible();

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByText('exit status 0')).toBeNull();
  });

  // A selection ends a mouse click on the line, not a press of the chevron:
  // Tab keeps the selection, and Enter on the chevron is a click.
  it('opens the details from the chevron while text is selected', () => {
    render(
      <ActivityLine
        line={{
          timestamp: '2026-08-06T12:00:00Z',
          level: 'INFO',
          message: 'step finished',
          details: 'exit status 0',
          structured: true,
        }}
        lineNumber={1}
      />
    );
    window.getSelection()?.selectAllChildren(screen.getByText('step finished'));
    const toggle = screen.getByRole('button', { name: 'Details' });
    try {
      fireEvent.click(toggle);
      expect(toggle).toHaveAttribute('aria-expanded', 'true');
      expect(screen.getByText('exit status 0')).toBeVisible();
    } finally {
      window.getSelection()?.removeAllRanges();
    }
  });

  it('has no chevron for a line without details', () => {
    render(
      <ActivityLine
        line={{
          timestamp: '2026-08-06T12:00:00Z',
          level: 'INFO',
          message: 'plain',
          structured: true,
        }}
        lineNumber={1}
      />
    );
    expect(screen.queryByRole('button')).toBeNull();
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

  // The log gets the height: the stream, the view, the line count, the
  // search, the jump and the display controls share one row (the dock gave
  // the log 35 to 75px with three stacked rows).
  it('keeps the controls in one toolbar row', () => {
    render(
      <StepLog
        dagName="example"
        dagRunId="run"
        stepName="build"
        node={{ status: NodeStatus.Success } as never}
      />,
      { wrapper: UserPreferencesProvider }
    );
    const toolbar = screen.getByTestId('step-log-toolbar');
    const row = toolbar.firstElementChild as HTMLElement;
    expect(toolbar.children).toHaveLength(1);
    for (const control of [
      screen.getByRole('button', { name: 'stdout' }),
      screen.getByRole('button', { name: 'Old to new' }),
      screen.getByText(/Showing 1 of 1 lines/),
      screen.getByPlaceholderText('Search in loaded lines...'),
      screen.getByLabelText('Jump to line:'),
      screen.getByRole('switch', { name: 'Wrap' }),
      screen.getByTitle('Download full log'),
    ]) {
      expect(row).toContainElement(control);
    }
    // One row wraps only when the width runs out; no mode label breaks.
    expect(row).toHaveClass('flex-wrap', 'items-center');
    expect(screen.getByRole('button', { name: 'Old to new' })).toHaveClass(
      'whitespace-nowrap'
    );
    expect(screen.getByRole('region', { name: 'Step output' })).toHaveClass(
      'min-h-0',
      'flex-1'
    );
  });

  it('switches between stdout and stderr in place', () => {
    render(
      <StepLog
        dagName="example"
        dagRunId="run"
        stepName="build"
        node={{ status: NodeStatus.Success } as never}
      />,
      { wrapper: UserPreferencesProvider }
    );
    expect(screen.getByRole('button', { name: 'stdout' })).toHaveAttribute(
      'aria-pressed',
      'true'
    );

    fireEvent.click(screen.getByRole('button', { name: 'stderr' }));

    expect(logs.queries[logs.queries.length - 1]).toMatchObject({
      stream: Stream.stderr,
    });
    expect(screen.getByRole('button', { name: 'stderr' })).toHaveAttribute(
      'aria-pressed',
      'true'
    );
  });

  // A caller that follows the tail owns the stream, so a switch must not
  // render the previous stream first.
  it('reads a caller-owned stream switch without the previous stream', () => {
    const { rerender } = render(
      <StepLog
        dagName="example"
        dagRunId="run"
        stepName="build"
        followTail
        stream={Stream.stdout}
      />,
      { wrapper: UserPreferencesProvider }
    );
    logs.queries = [];

    rerender(
      <StepLog
        dagName="example"
        dagRunId="run"
        stepName="build"
        followTail
        stream={Stream.stderr}
      />
    );

    expect(logs.queries.length).toBeGreaterThan(0);
    expect(logs.queries).toEqual(
      logs.queries.map((query) => ({ ...query, stream: Stream.stderr }))
    );
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

// New lines push the oldest ones out of a tail, so the view must hold still
// while someone reads it.
describe('ExecutionLog line count', () => {
  // No line of text under the Activity and Raw switch: the count is only
  // for screen readers.
  it('keeps the line count for screen readers only', () => {
    logs.data = {
      content: 'one\ntwo\nthree',
      totalLines: 3,
      lineCount: 3,
      hasMore: false,
    };
    render(
      <ExecutionLog
        name="example"
        dagRunId="run"
        dagRun={{ name: 'example', dagRunId: 'run' } as never}
      />,
      { wrapper: UserPreferencesProvider }
    );
    const count = screen.getByText('3 lines');
    expect(count.closest('.sr-only')).toBeInTheDocument();
    // A live run changes the count often: it is no live region.
    expect(count.closest('[role="status"]')).toBeNull();
  });
});

describe('ExecutionLog follow', () => {
  const runningRun = {
    name: 'example',
    dagRunId: 'run',
    status: Status.Running,
  } as never;

  it('holds the lines in place while reading and resumes on request', () => {
    logs.data = {
      content: 'scheduler started',
      totalLines: 1,
      lineCount: 1,
      hasMore: false,
    };
    const { rerender } = render(
      <ExecutionLog name="example" dagRunId="run" dagRun={runningRun} />,
      { wrapper: UserPreferencesProvider }
    );
    fireEvent.click(screen.getByRole('button', { name: 'Raw' }));
    const region = screen.getByRole('region', { name: 'Run log' });

    fireEvent.wheel(region, { deltaY: -120 });
    logs.data = {
      content: 'scheduler started\nstep done',
      totalLines: 2,
      lineCount: 2,
      hasMore: false,
    };
    rerender(
      <ExecutionLog name="example" dagRunId="run" dagRun={runningRun} />
    );

    expect(screen.queryByText('step done')).not.toBeInTheDocument();
    expect(screen.getByText('New output available')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Back to live' }));
    expect(screen.getByText('step done')).toBeInTheDocument();
  });

  it('keeps the newest lines in view when the display changes', () => {
    render(<ExecutionLog name="example" dagRunId="run" dagRun={runningRun} />, {
      wrapper: UserPreferencesProvider,
    });
    const region = screen.getByRole('region', { name: 'Run log' });
    Object.defineProperty(region, 'scrollHeight', {
      configurable: true,
      value: 1000,
    });
    region.scrollTop = 0;

    fireEvent.click(screen.getByRole('button', { name: 'Raw' }));

    expect(region.scrollTop).toBe(1000);
  });

  // The log stays mounted when the page moves to another run.
  it('drops the held lines when another run is shown', () => {
    logs.data = {
      content: 'first run line',
      totalLines: 1,
      lineCount: 1,
      hasMore: false,
    };
    const { rerender } = render(
      <ExecutionLog name="example" dagRunId="run" dagRun={runningRun} />,
      { wrapper: UserPreferencesProvider }
    );
    fireEvent.click(screen.getByRole('button', { name: 'Raw' }));
    fireEvent.wheel(screen.getByRole('region', { name: 'Run log' }), {
      deltaY: -120,
    });

    logs.data = {
      content: 'second run line',
      totalLines: 1,
      lineCount: 1,
      hasMore: false,
    };
    rerender(
      <ExecutionLog
        name="example"
        dagRunId="run-2"
        dagRun={{ ...(runningRun as object), dagRunId: 'run-2' } as never}
      />
    );

    expect(screen.getByText('second run line')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Back to live' })
    ).not.toBeInTheDocument();
  });

  // A tail of repeated lines can look the same after a line is added.
  it('announces new lines when the held tail reads the same', () => {
    logs.data = {
      content: 'tick\ntick',
      totalLines: 2,
      lineCount: 2,
      hasMore: true,
    };
    const { rerender } = render(
      <ExecutionLog name="example" dagRunId="run" dagRun={runningRun} />,
      { wrapper: UserPreferencesProvider }
    );
    fireEvent.wheel(screen.getByRole('region', { name: 'Run log' }), {
      deltaY: -120,
    });

    logs.data = { ...logs.data, totalLines: 3 };
    rerender(
      <ExecutionLog name="example" dagRunId="run" dagRun={runningRun} />
    );

    expect(screen.getByText('New output available')).toBeInTheDocument();
  });

  // The controls keep their height and the log region may shrink below its
  // content, so that a short dock scrolls the rows and never clips the header.
  it('keeps the controls whole and scrolls the rows inside the region', () => {
    render(
      <ExecutionLog
        name="example"
        dagRunId="run"
        dagRun={{ ...(runningRun as object), status: Status.Success } as never}
      />,
      { wrapper: UserPreferencesProvider }
    );
    const region = screen.getByRole('region', { name: 'Run log' });
    expect(region).toHaveClass('min-h-0', 'flex-1', 'overflow-auto');
    const controls = screen.getByRole('group', { name: 'Log display' })
      .parentElement!.parentElement!;
    expect(controls).toHaveClass('shrink-0');
  });

  it('keeps a finished log where its reader is when the display changes', () => {
    render(
      <ExecutionLog
        name="example"
        dagRunId="run"
        dagRun={{ ...(runningRun as object), status: Status.Success } as never}
      />,
      { wrapper: UserPreferencesProvider }
    );
    const region = screen.getByRole('region', { name: 'Run log' });
    Object.defineProperty(region, 'scrollHeight', {
      configurable: true,
      value: 1000,
    });
    region.scrollTop = 200;

    fireEvent.click(screen.getByRole('button', { name: 'Raw' }));

    expect(region.scrollTop).toBe(200);
  });

  // A finished log opens on its newest lines, but a reader who moves keeps
  // their place when the lines are fetched again.
  it('follows a finished log until its reader moves', () => {
    const finishedRun = {
      ...(runningRun as object),
      status: Status.Success,
    } as never;
    const { rerender } = render(
      <ExecutionLog name="example" dagRunId="run" dagRun={finishedRun} />,
      { wrapper: UserPreferencesProvider }
    );
    const region = screen.getByRole('region', { name: 'Run log' });
    Object.defineProperty(region, 'scrollHeight', {
      configurable: true,
      value: 1000,
    });
    logs.data = { ...logs.data, content: 'first output\nlast output' };
    rerender(
      <ExecutionLog name="example" dagRunId="run" dagRun={finishedRun} />
    );
    expect(region.scrollTop).toBe(1000);

    region.scrollTop = 200;
    fireEvent.wheel(region, { deltaY: -120 });
    logs.data = { ...logs.data, content: 'first output\nlast output\nfooter' };
    rerender(
      <ExecutionLog name="example" dagRunId="run" dagRun={finishedRun} />
    );

    expect(region.scrollTop).toBe(200);
    expect(
      screen.queryByRole('button', { name: 'Back to live' })
    ).not.toBeInTheDocument();
  });

  it('shows another range instead of the held lines', () => {
    logs.data = {
      content: 'line 9\nline 10',
      totalLines: 10,
      lineCount: 2,
      hasMore: true,
    };
    // A page of the log: the lines at an offset.
    logs.head = { content: 'line 1\nline 2' };
    render(<ExecutionLog name="example" dagRunId="run" dagRun={runningRun} />, {
      wrapper: UserPreferencesProvider,
    });
    fireEvent.click(screen.getByRole('button', { name: 'Raw' }));
    fireEvent.wheel(screen.getByRole('region', { name: 'Run log' }), {
      deltaY: -120,
    });

    fireEvent.click(screen.getByRole('button', { name: 'Page View' }));

    expect(screen.getByText('line 1')).toBeInTheDocument();
    expect(screen.queryByText('line 10')).not.toBeInTheDocument();
  });
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
      expect(logs.queries[logs.queries.length - 1]).toMatchObject({
        offset: 3001,
        limit: 1000,
      });
    } finally {
      vi.useRealTimers();
    }
  });

  // Without the Page View switch only the tail shows: no jump opens a page
  // that the reader cannot leave.
  it.each(['run', 'step'])(
    'offers no jump in a %s log without Page View',
    (log) => {
      logs.data = { ...pagedData };
      render(
        log === 'run' ? (
          <ExecutionLog name="example" dagRunId="run" pageView={false} />
        ) : (
          <StepLog
            dagName="example"
            dagRunId="run"
            stepName="build"
            pageView={false}
          />
        ),
        { wrapper: UserPreferencesProvider }
      );
      expect(screen.getByLabelText('Lines per page')).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Page View' })).toBeNull();
      expect(screen.queryByText('Jump to line:')).toBeNull();
      expect(screen.queryByRole('spinbutton')).toBeNull();
    }
  );

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
      expect(logs.queries[logs.queries.length - 1]).toMatchObject({
        offset: 2001,
        limit: 1000,
      });
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

// The order toggle puts the oldest or the newest line at the top. It keeps
// the choice in the browser.
describe('log order toggle', () => {
  const rows = (container: HTMLElement) =>
    Array.from(container.querySelectorAll('[data-line-number]')).map(
      (el) => el.nextElementSibling?.textContent
    );
  beforeEach(() => {
    localStorage.clear();
    logs.data = {
      content: 'alpha\nbeta\ngamma',
      totalLines: 3,
      lineCount: 3,
      hasMore: false,
    };
  });

  it('reverses the lines of a step log and flips the chevron', () => {
    const { container } = render(
      <StepLog
        dagName="example"
        dagRunId="run"
        stepName="build"
        node={{ status: NodeStatus.Success } as never}
      />,
      { wrapper: UserPreferencesProvider }
    );
    expect(rows(container)).toEqual(['alpha', 'beta', 'gamma']);
    const button = screen.getByRole('button', { name: 'Old to new' });
    expect(button.querySelector('svg')).not.toHaveClass('rotate-180');

    fireEvent.click(button);

    expect(rows(container)).toEqual(['gamma', 'beta', 'alpha']);
    // The line numbers stay with their lines.
    expect(
      Array.from(container.querySelectorAll('[data-line-number]')).map((el) =>
        el.getAttribute('data-line-number')
      )
    ).toEqual(['3', '2', '1']);
    const flipped = screen.getByRole('button', { name: 'New to old' });
    expect(flipped.querySelector('svg')).toHaveClass('rotate-180');
  });

  it('keeps the button in a slot of fixed width', () => {
    render(
      <StepLog
        dagName="example"
        dagRunId="run"
        stepName="build"
        node={{ status: NodeStatus.Success } as never}
      />,
      { wrapper: UserPreferencesProvider }
    );
    const before = screen.getByRole('button', { name: 'Old to new' }).className;
    fireEvent.click(screen.getByRole('button', { name: 'Old to new' }));
    const after = screen.getByRole('button', { name: 'New to old' }).className;
    expect(before).toContain('w-28');
    expect(after).toContain('w-28');
  });

  it('keeps the choice for the next viewer in the browser', () => {
    const view = render(
      <ExecutionLog name="example" dagRunId="run" dagRun={undefined} />,
      { wrapper: UserPreferencesProvider }
    );
    fireEvent.click(screen.getByRole('button', { name: 'Old to new' }));
    expect(
      JSON.parse(localStorage.getItem('user_preferences') ?? '{}').logOrder
    ).toBe('newToOld');
    view.unmount();

    render(<ExecutionLog name="example" dagRunId="run" dagRun={undefined} />, {
      wrapper: UserPreferencesProvider,
    });
    expect(screen.getByRole('button', { name: 'New to old' })).toBeVisible();
  });

  it('reverses the raw lines of the run log', () => {
    const { container } = render(
      <ExecutionLog name="example" dagRunId="run" dagRun={undefined} />,
      { wrapper: UserPreferencesProvider }
    );
    fireEvent.click(screen.getByRole('button', { name: 'Raw' }));
    expect(rows(container)).toEqual(['alpha', 'beta', 'gamma']);
    fireEvent.click(screen.getByRole('button', { name: 'Old to new' }));
    expect(rows(container)).toEqual(['gamma', 'beta', 'alpha']);
  });

  it('puts the cached lines before the lines of the run, or after them for new to old', () => {
    const { container } = render(
      <ExecutionLog
        name="example"
        dagRunId="run"
        dagRun={undefined}
        cachedLines={<div data-testid="cached">cached</div>}
      />,
      { wrapper: UserPreferencesProvider }
    );
    const region = screen.getByRole('region', { name: 'Run log' });
    const cached = screen.getByTestId('cached');
    expect(region).toContainElement(cached);
    const lines = () => container.querySelector('[data-log-chunk]')!;
    expect(
      cached.compareDocumentPosition(lines()) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'Old to new' }));

    expect(
      lines().compareDocumentPosition(screen.getByTestId('cached')) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  it('follows the bottom for old to new and the top for new to old', () => {
    render(
      <StepLog
        dagName="example"
        dagRunId="run"
        stepName="build"
        node={{ status: NodeStatus.Success } as never}
      />,
      { wrapper: UserPreferencesProvider }
    );
    const region = screen.getByRole('region', { name: 'Step output' });
    Object.defineProperty(region, 'scrollHeight', {
      configurable: true,
      value: 800,
    });
    region.scrollTop = 300;

    fireEvent.click(screen.getByRole('button', { name: 'Old to new' }));
    expect(region.scrollTop).toBe(0);

    fireEvent.click(screen.getByRole('button', { name: 'New to old' }));
    expect(region.scrollTop).toBe(800);
  });

  // A new line shifts every row of the newest-first view down by one.
  it('keeps the open details with their line when a new line arrives', () => {
    const line = (msg: string, code: number) =>
      JSON.stringify({
        time: '2026-08-06T12:00:00Z',
        level: 'INFO',
        msg,
        code,
      });
    const runningRun = {
      name: 'example',
      dagRunId: 'run',
      status: Status.Running,
    } as never;
    logs.data = {
      content: [line('first', 1), line('second', 2)].join('\n'),
      totalLines: 2,
      lineCount: 2,
      hasMore: false,
    };
    const { rerender } = render(
      <ExecutionLog name="example" dagRunId="run" dagRun={runningRun} />,
      { wrapper: UserPreferencesProvider }
    );
    fireEvent.click(screen.getByRole('button', { name: 'Old to new' }));
    const row = (msg: string) =>
      screen.getByText(msg).closest<HTMLElement>('[data-line-number]')!;
    fireEvent.click(
      within(row('first')).getByRole('button', { name: 'Details' })
    );
    expect(screen.getByText('code=1')).toBeVisible();

    logs.data = {
      content: [line('first', 1), line('second', 2), line('third', 3)].join(
        '\n'
      ),
      totalLines: 3,
      lineCount: 3,
      hasMore: false,
    };
    rerender(
      <ExecutionLog name="example" dagRunId="run" dagRun={runningRun} />
    );

    expect(screen.getByText('third')).toBeInTheDocument();
    expect(within(row('first')).getByText('code=1')).toBeVisible();
    expect(screen.queryByText('code=2')).toBeNull();
  });
});
