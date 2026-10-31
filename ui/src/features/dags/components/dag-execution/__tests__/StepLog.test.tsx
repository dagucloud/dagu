// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Stream } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import StepLog from '../StepLog';

type Log = { content: string; lineCount: number; totalLines: number };

// A response keeps its identity across renders, as SWR's does. A stream with
// no entry is still loading: no data, no error.
const api = vi.hoisted(() => ({
  logs: {} as Record<string, unknown>,
  errors: {} as Record<string, Error>,
  streams: [] as string[],
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ apiURL: '/api/v1' }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/hooks/useStepLogSSE', () => ({
  useStepLogSSE: () => ({
    data: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: true,
    error: null,
  }),
}));
vi.mock('@/hooks/api', () => ({
  useQuery: (
    _path: string,
    init: { params: { query: { stream: string } } } | null
  ) => {
    if (!init) {
      return { data: undefined, isLoading: false, mutate: vi.fn() };
    }
    const { stream } = init.params.query;
    api.streams.push(stream);
    const data = api.logs[stream];
    const error = api.errors[stream];
    return {
      data,
      error,
      isLoading: data === undefined && !error,
      mutate: () => Promise.resolve(undefined),
    };
  },
}));

function log(prefix: string): Log {
  return {
    content: `${prefix} line 1\n${prefix} line 2\n`,
    lineCount: 2,
    totalLines: 2,
  };
}

const props = {
  dagName: 'example',
  dagRunId: 'run-1',
  stepName: 'build',
  status: NodeStatus.Success,
};

function renderLog(extra: Partial<React.ComponentProps<typeof StepLog>> = {}) {
  return render(<StepLog {...props} {...extra} />, {
    wrapper: UserPreferencesProvider,
  });
}

const switchTo = (stream: 'stdout' | 'stderr') =>
  fireEvent.click(screen.getByRole('button', { name: stream }));

beforeEach(() => {
  api.logs = { stdout: log('out'), stderr: log('err') };
  api.errors = {};
  api.streams = [];
});

// The viewer must not flash when the reader switches streams: the previous
// content and the toolbar stay in the document while the other stream loads.
describe('StepLog stream switch', () => {
  it('keeps the previous stream shown while the new one is pending', () => {
    delete api.logs.stderr;
    renderLog();
    expect(screen.getByText('out line 1')).toBeInTheDocument();
    const region = screen.getByRole('region', { name: 'Step output' });

    switchTo('stderr');

    expect(api.streams[api.streams.length - 1]).toBe('stderr');
    expect(screen.getByRole('region', { name: 'Step output' })).toBe(region);
    expect(screen.getByText('out line 1')).toBeInTheDocument();
    expect(screen.getByRole('group', { name: 'Stream' })).toBeVisible();
    expect(screen.queryByText('No output recorded.')).toBeNull();
  });

  it('swaps in the new stream when it arrives, in the same viewer', () => {
    delete api.logs.stderr;
    const view = renderLog();
    const region = screen.getByRole('region', { name: 'Step output' });
    switchTo('stderr');

    api.logs.stderr = log('err');
    view.rerender(<StepLog {...props} />);

    expect(screen.getByText('err line 1')).toBeInTheDocument();
    expect(screen.queryByText('out line 1')).toBeNull();
    expect(screen.getByRole('region', { name: 'Step output' })).toBe(region);
  });

  it('keeps the previous stream when the caller owns the stream', () => {
    delete api.logs.stderr;
    const view = renderLog({
      stream: Stream.stdout,
      followTail: true,
    });
    expect(screen.getByText('out line 1')).toBeInTheDocument();

    view.rerender(
      <StepLog {...props} stream={Stream.stderr} followTail={true} />
    );

    expect(screen.getByText('out line 1')).toBeInTheDocument();
  });

  it('shows no content of the previous stream once the new one fails', () => {
    delete api.logs.stderr;
    const view = renderLog();
    switchTo('stderr');

    api.errors.stderr = new Error('log file not found');
    view.rerender(<StepLog {...props} />);

    expect(screen.queryByText('out line 1')).toBeNull();
    expect(screen.getByText('No output recorded.')).toBeInTheDocument();
  });

  // A pause while the new stream loads freezes the new stream once it comes,
  // never the content of the previous one that stands in for it.
  it('shows the new stream after a pause while it loads', () => {
    delete api.logs.stderr;
    const view = renderLog();
    switchTo('stderr');
    fireEvent.focus(screen.getByPlaceholderText('Search in loaded lines...'));

    api.logs.stderr = log('err');
    view.rerender(<StepLog {...props} />);
    expect(screen.getByText('err line 1')).toBeInTheDocument();
    expect(screen.queryByText('out line 1')).toBeNull();

    api.logs.stderr = log('later');
    view.rerender(<StepLog {...props} />);
    expect(screen.getByText('err line 1')).toBeInTheDocument();
  });

  it('ends the search of the previous stream', () => {
    renderLog();
    const search = screen.getByPlaceholderText('Search in loaded lines...');
    fireEvent.change(search, { target: { value: 'line 2' } });
    expect(screen.getByText('1/1')).toBeInTheDocument();

    switchTo('stderr');

    expect(
      screen.getByPlaceholderText('Search in loaded lines...')
    ).toHaveValue('');
  });
});
